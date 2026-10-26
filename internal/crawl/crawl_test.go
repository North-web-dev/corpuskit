package crawl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/North-web-dev/corpuskit/internal/store"
)

func testSite() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("User-agent: *\nDisallow: /private/\n"))
	})
	page := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(body))
		}
	}
	mux.HandleFunc("/", page(`<title>Home</title><body>
		<p>Welcome to the polite crawler test site about search indexing.</p>
		<a href="/a">A</a> <a href="/b">B</a>
		<a href="/private/secret">secret</a>
		<a href="http://off-scope.invalid/x">external</a>
		<a href="mailto:me@example.com">mail</a>
		<a href="/img.png">image</a>`))
	mux.HandleFunc("/a", page(`<title>Page A</title><body><p>Page A discusses BM25 ranking in depth.</p><a href="/b">B</a><a href="/c">C</a></body>`))
	mux.HandleFunc("/b", page(`<title>Page B</title><body><p>Page B is a leaf about robots and provenance.</p></body>`))
	mux.HandleFunc("/c", page(`<title>Page C</title><body><p>Page C is one level deeper.</p></body>`))
	mux.HandleFunc("/private/secret", page(`<title>Secret</title><body><p>should never be indexed</p></body>`))
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG not html"))
	})
	return httptest.NewServer(mux)
}

func crawlInto(t *testing.T, start string, opts Options) (*store.Store, Stats) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	opts.Rate = 1000
	if opts.Concurrency == 0 {
		opts.Concurrency = 4
	}
	stats, err := Crawl(context.Background(), start, st, opts)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	return st, stats
}

func TestCrawlRespectsRobotsScopeAndExtractsText(t *testing.T) {
	srv := testSite()
	defer srv.Close()
	st, stats := crawlInto(t, srv.URL, Options{MaxPages: 100, MaxDepth: 5})

	ctx := context.Background()
	// robots.txt disallowed /private/: nothing from it may be indexed.
	if res, _ := st.Search(ctx, "should never be indexed", 10); len(res) != 0 {
		t.Errorf("robots-disallowed page was indexed")
	}
	if stats.Skipped == 0 {
		t.Errorf("expected at least one skip (robots/non-html)")
	}
	// In-scope pages were extracted into FTS.
	if res, _ := st.Search(ctx, "BM25 ranking", 10); len(res) == 0 {
		t.Errorf("expected Page A text in index")
	}
	if res, _ := st.Search(ctx, "provenance", 10); len(res) == 0 {
		t.Errorf("expected Page B text in index")
	}
	// Non-HTML and off-scope/non-http links were never stored.
	n, _ := st.Count(ctx)
	if n != 4 { // home, /a, /b, /c
		t.Errorf("indexed %d docs, want 4 (home,a,b,c)", n)
	}
}

func TestCrawlMaxPages(t *testing.T) {
	srv := testSite()
	defer srv.Close()
	st, stats := crawlInto(t, srv.URL, Options{MaxPages: 2, MaxDepth: 5})
	n, _ := st.Count(context.Background())
	if n > 2 {
		t.Errorf("max-pages not enforced: %d docs", n)
	}
	if stats.Stored > 2 {
		t.Errorf("stored %d, want <= 2", stats.Stored)
	}
}

func TestCrawlMaxDepth(t *testing.T) {
	srv := testSite()
	defer srv.Close()
	// Depth 0 stores only the start page.
	st, _ := crawlInto(t, srv.URL, Options{MaxPages: 100, MaxDepth: 0})
	if n, _ := st.Count(context.Background()); n != 1 {
		t.Errorf("depth 0 indexed %d docs, want 1", n)
	}
}

func TestCrawlIgnoreRobots(t *testing.T) {
	srv := testSite()
	defer srv.Close()
	st, _ := crawlInto(t, srv.URL, Options{MaxPages: 100, MaxDepth: 5, IgnoreRobots: true})
	if res, _ := st.Search(context.Background(), "should never be indexed", 10); len(res) == 0 {
		t.Errorf("with --ignore-robots the private page should be reachable")
	}
}

func TestCrawlBadStart(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	if _, err := Crawl(context.Background(), "ftp://x/y", st, Options{}); err == nil {
		t.Error("expected error for non-http(s) start URL")
	}
}

func TestNormalizeURL(t *testing.T) {
	u, err := normalize("HTTP://user:pass@Example.COM:80/path#frag")
	if err != nil {
		t.Fatal(err)
	}
	if u.User != nil {
		t.Error("credentials not stripped")
	}
	if u.Host != "example.com" {
		t.Errorf("host = %q, want example.com (lowered, default port dropped)", u.Host)
	}
	if u.Fragment != "" {
		t.Error("fragment not stripped")
	}
}
