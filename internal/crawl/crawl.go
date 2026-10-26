// Package crawl implements a polite, scope-limited web crawler: it obeys
// robots.txt (Disallow and Crawl-delay), rate-limits per host, caps pages,
// depth and body size, stays on the start host by default, and stores each
// page's readable text together with its source URL for provenance.
package crawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/North-web-dev/corpuskit/internal/extract"
	"github.com/North-web-dev/corpuskit/internal/httpclient"
	"github.com/North-web-dev/corpuskit/internal/store"
)

// Options configures a crawl.
type Options struct {
	MaxPages     int
	MaxDepth     int
	Rate         float64 // requests per second (per host)
	Concurrency  int
	UserAgent    string
	Browser      bool
	IgnoreRobots bool
	MaxBodyBytes int64
	Timeout      time.Duration
	Logf         func(format string, args ...any)
}

// Stats summarizes a completed crawl.
type Stats struct {
	Stored  int
	Fetched int
	Skipped int
}

// DefaultUserAgent identifies the tool honestly.
const DefaultUserAgent = "corpuskit/0.1 (+https://github.com/North-web-dev/corpuskit)"

var errOffScope = errors.New("redirect off scope")

// Crawl walks the site starting at start, storing pages into st. It returns
// when the frontier is exhausted or a cap is hit.
func Crawl(ctx context.Context, start string, st *store.Store, opts Options) (Stats, error) {
	var stats Stats
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultUserAgent
	}
	if opts.Concurrency < 1 {
		opts.Concurrency = 1
	}
	if opts.Rate <= 0 {
		opts.Rate = 1
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 5 << 20
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}

	startURL, err := normalize(start)
	if err != nil {
		return stats, fmt.Errorf("invalid start URL: %w", err)
	}
	scopeHost := startURL.Host

	redirect := func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if req.URL.Host != scopeHost || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return errOffScope
		}
		return nil
	}
	client := httpclient.New(opts.Browser, opts.Timeout, redirect)

	lim := &limiter{interval: time.Duration(float64(time.Second) / opts.Rate)}

	rob := robots{found: false}
	if opts.IgnoreRobots {
		opts.Logf("WARNING: --ignore-robots is set; robots.txt will NOT be honored")
	} else {
		rob = fetchRobots(ctx, client, lim, startURL, opts)
		if rob.crawlDelay > lim.interval {
			opts.Logf("robots.txt Crawl-delay %s exceeds --rate; using %s", rob.crawlDelay, rob.crawlDelay)
			lim.interval = rob.crawlDelay
		}
	}

	visited := map[string]bool{startURL.String(): true}
	frontier := []*url.URL{startURL}

	for depth := 0; depth <= opts.MaxDepth && len(frontier) > 0; depth++ {
		if stats.Stored >= opts.MaxPages {
			break
		}
		var (
			mu   sync.Mutex
			next []*url.URL
			wg   sync.WaitGroup
			sem  = make(chan struct{}, opts.Concurrency)
		)
		for _, u := range frontier {
			mu.Lock()
			over := stats.Stored >= opts.MaxPages
			mu.Unlock()
			if over {
				break
			}
			if !opts.IgnoreRobots && !rob.allowed(u.EscapedPath()) {
				mu.Lock()
				stats.Skipped++
				mu.Unlock()
				opts.Logf("skip (robots): %s", u)
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(u *url.URL) {
				defer wg.Done()
				defer func() { <-sem }()
				if err := lim.wait(ctx); err != nil {
					return
				}
				doc, hasDoc, links, skipped := fetchPage(ctx, client, u, opts, depth)
				mu.Lock()
				defer mu.Unlock()
				stats.Fetched++
				if skipped {
					stats.Skipped++
				}
				if hasDoc && stats.Stored < opts.MaxPages {
					if _, err := st.Upsert(ctx, doc); err != nil {
						opts.Logf("skip (store error): %s: %v", u, err)
						stats.Skipped++
					} else {
						stats.Stored++
						opts.Logf("stored [d%d]: %s (%d chars)", depth, u, len(doc.Text))
					}
				}
				if depth < opts.MaxDepth {
					for _, l := range links {
						if l.Host == scopeHost && !visited[l.String()] {
							visited[l.String()] = true
							next = append(next, l)
						}
					}
				}
			}(u)
		}
		wg.Wait()
		frontier = next
	}
	return stats, nil
}

// fetchPage fetches one URL and, if it is HTML, returns the extracted document
// and the in-scope links it found. It never writes to the store; the caller
// decides whether to persist (so the page cap stays a hard limit). hasDoc is
// false when the page was skipped (non-200, non-HTML, read/parse error).
func fetchPage(ctx context.Context, client *http.Client, u *url.URL, opts Options, depth int) (doc store.Document, hasDoc bool, links []*url.URL, skipped bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		opts.Logf("skip (bad request): %s", logURL(u))
		return doc, false, nil, true
	}
	req.Header.Set("User-Agent", opts.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		opts.Logf("skip (fetch error): %s: %v", logURL(u), redactErr(err))
		return doc, false, nil, true
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		opts.Logf("skip (HTTP %d): %s", resp.StatusCode, logURL(u))
		return doc, false, nil, true
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(strings.ToLower(ct), "text/html") {
		opts.Logf("skip (non-HTML %q): %s", ct, logURL(u))
		return doc, false, nil, true
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, opts.MaxBodyBytes))
	if err != nil {
		opts.Logf("skip (read error): %s", logURL(u))
		return doc, false, nil, true
	}
	res, err := extract.HTML(string(body))
	if err != nil {
		opts.Logf("skip (parse error): %s", logURL(u))
		return doc, false, nil, true
	}
	title := res.Title
	if title == "" {
		title = u.String()
	}
	doc = store.Document{Source: u.String(), Title: title, Text: res.Text}

	for _, href := range res.Links {
		ref, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			continue
		}
		n, err := normalizeURL(u.ResolveReference(ref))
		if err != nil {
			continue
		}
		links = append(links, n)
	}
	return doc, true, links, false
}

// fetchRobots retrieves and parses robots.txt for the start host. A missing
// file (4xx) means full access; 401/403 means fully restricted; a server error
// or network failure is treated as full access with a warning.
func fetchRobots(ctx context.Context, client *http.Client, lim *limiter, start *url.URL, opts Options) robots {
	ru := &url.URL{Scheme: start.Scheme, Host: start.Host, Path: "/robots.txt"}
	if err := lim.wait(ctx); err != nil {
		return robots{found: false}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ru.String(), nil)
	if err != nil {
		return robots{found: false}
	}
	req.Header.Set("User-Agent", opts.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		opts.Logf("robots.txt unreachable (%v); proceeding", redactErr(err))
		return robots{found: false}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return parseRobots(string(body), agentToken(opts.UserAgent))
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		opts.Logf("robots.txt returned %d; treating site as disallowed", resp.StatusCode)
		return robots{disallow: []string{"/"}, found: true}
	default:
		opts.Logf("no usable robots.txt (HTTP %d); proceeding", resp.StatusCode)
		return robots{found: false}
	}
}

// agentToken reduces a full User-Agent string to its leading product token for
// robots.txt group matching.
func agentToken(ua string) string {
	ua = strings.TrimSpace(ua)
	if i := strings.IndexAny(ua, " /"); i > 0 {
		return ua[:i]
	}
	return ua
}

// limiter enforces a minimum interval between requests across goroutines.
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	wait := l.interval - now.Sub(l.last)
	if wait < 0 {
		wait = 0
	}
	l.last = now.Add(wait)
	l.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// normalize parses and canonicalizes a raw URL string.
func normalize(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return normalizeURL(u)
}

// normalizeURL canonicalizes a parsed URL: requires an http(s) scheme, strips
// the fragment and any embedded credentials, lowercases the host and drops a
// default port.
func normalizeURL(u *url.URL) (*url.URL, error) {
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("non-http(s) scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("missing host")
	}
	c := *u
	c.Fragment = ""
	c.User = nil
	c.Host = strings.ToLower(c.Host)
	if (c.Scheme == "http" && strings.HasSuffix(c.Host, ":80")) ||
		(c.Scheme == "https" && strings.HasSuffix(c.Host, ":443")) {
		c.Host = c.Host[:strings.LastIndex(c.Host, ":")]
	}
	if c.Path == "" {
		c.Path = "/"
	}
	return &c, nil
}

// logURL renders a URL for logs with any credentials removed.
func logURL(u *url.URL) string {
	c := *u
	c.User = nil
	return c.String()
}

// credsRe matches a userinfo component in a URL (user:pass@host).
var credsRe = regexp.MustCompile(`//[^/@\s]*@`)

// redactErr strips any embedded credentials from URLs in an error message.
func redactErr(err error) string {
	if err == nil {
		return ""
	}
	return credsRe.ReplaceAllString(err.Error(), "//")
}
