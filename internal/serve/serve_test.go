package serve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/North-web-dev/corpuskit/internal/store"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st.Upsert(ctx, store.Document{Source: "http://x/a", Title: "Clean Page", Text: "full text search with bm25 ranking"})
	// Hostile content: a title/text carrying a script tag, plus a
	// javascript: source. Nothing here may become live markup.
	st.Upsert(ctx, store.Document{
		Source: "javascript:alert(1)",
		Title:  `<script>alert('xss')</script>`,
		Text:   `pwned <img src=x onerror=alert(1)> bm25 injection attempt`,
	})
	srv := httptest.NewServer(New(st))
	t.Cleanup(func() { srv.Close(); st.Close() })
	return srv
}

func TestSearchJSON(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/search?q=bm25")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	var out apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Count < 1 || out.Query != "bm25" {
		t.Fatalf("unexpected response: %+v", out)
	}
}

func TestSearchPageEscapesContent(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/?q=bm25")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := readAll(resp)

	// The raw script tag from indexed content must NOT appear verbatim.
	if strings.Contains(body, "<script>alert('xss')</script>") {
		t.Fatal("unescaped <script> from indexed title leaked into page")
	}
	// It must appear escaped instead.
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("expected escaped script tag in output")
	}
	if strings.Contains(body, "onerror=alert(1)>") {
		t.Fatal("unescaped img payload leaked into page")
	}
	// A javascript: URL must be neutralized by html/template URL filtering.
	if strings.Contains(body, `href="javascript:alert(1)"`) {
		t.Fatal("javascript: URL was not filtered")
	}
}

func TestHostileQueryEndpoint(t *testing.T) {
	srv := newServer(t)
	for _, q := range []string{`" OR 1=1 --`, `NEAR(a b)`, `*`, `)))(((`} {
		resp, err := http.Get(srv.URL + "/search?q=" + urlQuery(q))
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("query %q: status %d", q, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestLoopbackAddr(t *testing.T) {
	for _, a := range []string{"127.0.0.1:8080", "localhost:9000", "[::1]:8080"} {
		if !LoopbackAddr(a) {
			t.Errorf("%q should be loopback", a)
		}
	}
	for _, a := range []string{"0.0.0.0:8080", "192.168.1.5:80", "example.com:80"} {
		if LoopbackAddr(a) {
			t.Errorf("%q should NOT be loopback", a)
		}
	}
}

func readAll(resp *http.Response) (string, error) {
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func urlQuery(s string) string {
	r := strings.NewReplacer(" ", "%20", `"`, "%22", "(", "%28", ")", "%29", "*", "%2A", "=", "%3D")
	return r.Replace(s)
}
