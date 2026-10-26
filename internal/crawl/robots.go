package crawl

import (
	"bufio"
	"strconv"
	"strings"
	"time"
)

// robots is a parsed robots.txt, reduced to the rules that apply to our agent.
type robots struct {
	disallow   []string
	allow      []string
	crawlDelay time.Duration
	found      bool // true if a group matched our agent (or *)
}

// parseRobots parses a robots.txt body and returns the rules for the given
// user-agent token, falling back to the "*" group. Matching is the standard
// most-specific-prefix behaviour; this parser implements the common subset
// (User-agent, Disallow, Allow, Crawl-delay) and is case-insensitive on
// directives.
func parseRobots(body, agent string) robots {
	agent = strings.ToLower(agent)
	type group struct {
		agents     []string
		disallow   []string
		allow      []string
		crawlDelay time.Duration
		hasDelay   bool
	}
	var groups []*group
	var cur *group
	sawDirective := false

	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "user-agent":
			if cur == nil || sawDirective {
				cur = &group{}
				groups = append(groups, cur)
				sawDirective = false
			}
			cur.agents = append(cur.agents, strings.ToLower(val))
		case "disallow":
			if cur != nil {
				cur.disallow = append(cur.disallow, val)
				sawDirective = true
			}
		case "allow":
			if cur != nil {
				cur.allow = append(cur.allow, val)
				sawDirective = true
			}
		case "crawl-delay":
			if cur != nil {
				if f, err := strconv.ParseFloat(val, 64); err == nil && f >= 0 {
					cur.crawlDelay = time.Duration(f * float64(time.Second))
					cur.hasDelay = true
				}
				sawDirective = true
			}
		}
	}

	pick := func(name string) *group {
		for _, g := range groups {
			for _, a := range g.agents {
				if a == name {
					return g
				}
			}
		}
		return nil
	}
	g := pick(agent)
	if g == nil {
		g = pick("*")
	}
	if g == nil {
		return robots{found: false}
	}
	return robots{disallow: g.disallow, allow: g.allow, crawlDelay: g.crawlDelay, found: true}
}

// allowed reports whether path may be fetched. An explicit Allow match that is
// at least as specific as the longest Disallow match wins; an empty Disallow
// value means "disallow nothing".
func (r robots) allowed(path string) bool {
	if !r.found {
		return true
	}
	longestDis := -1
	for _, d := range r.disallow {
		if d == "" {
			continue
		}
		if strings.HasPrefix(path, d) && len(d) > longestDis {
			longestDis = len(d)
		}
	}
	if longestDis < 0 {
		return true
	}
	for _, a := range r.allow {
		if a != "" && strings.HasPrefix(path, a) && len(a) >= longestDis {
			return true
		}
	}
	return false
}
