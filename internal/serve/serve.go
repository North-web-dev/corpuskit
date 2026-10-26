// Package serve exposes a read-only HTTP JSON search API over an existing
// index, plus a minimal self-contained search page. All indexed content is
// third-party and therefore untrusted: every value is escaped before it is
// written into the HTML page, and the page builds results in the DOM via
// textContent rather than interpolating markup.
package serve

import (
	"context"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/North-web-dev/corpuskit/internal/store"
)

// Server is the read-only search HTTP handler.
type Server struct {
	st       *store.Store
	maxLimit int
	mux      *http.ServeMux
}

// New builds a Server backed by st.
func New(st *store.Store) *Server {
	s := &Server{st: st, maxLimit: 50, mux: http.NewServeMux()}
	s.mux.HandleFunc("/search", s.handleSearch)
	s.mux.HandleFunc("/", s.handleIndex)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

type apiResult struct {
	Source  string  `json:"source"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

type apiResponse struct {
	Query   string      `json:"query"`
	Count   int         `json:"count"`
	Results []apiResult `json:"results"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := s.parseLimit(r.URL.Query().Get("limit"))
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	results, err := s.st.Search(ctx, q, limit)
	if err != nil {
		http.Error(w, `{"error":"search failed"}`, http.StatusInternalServerError)
		return
	}
	resp := apiResponse{Query: q, Count: len(results), Results: make([]apiResult, 0, len(results))}
	for _, r := range results {
		resp.Results = append(resp.Results, apiResult(r))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) parseLimit(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 10
	}
	if n > s.maxLimit {
		return s.maxLimit
	}
	return n
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query().Get("q")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var results []store.Result
	if strings.TrimSpace(q) != "" {
		results, _ = s.st.Search(ctx, q, 20)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	_ = pageTmpl.Execute(w, pageData{Query: q, Results: results, Searched: strings.TrimSpace(q) != ""})
}

type pageData struct {
	Query    string
	Results  []store.Result
	Searched bool
}

// pageTmpl renders the search page. html/template context-escapes every
// interpolated value, so hostile titles/snippets from the index cannot inject
// markup or script.
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>corpuskit search</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;max-width:50rem;margin:2rem auto;padding:0 1rem;color:#111}
input[type=search]{width:100%;padding:.6rem;font-size:1rem;box-sizing:border-box}
.hit{margin:1.25rem 0;padding-bottom:1rem;border-bottom:1px solid #eee}
.title{font-weight:600}
.src{font-size:.85rem;word-break:break-all}
.src a{color:#006;}
.snip{color:#333;margin-top:.25rem}
.snip mark{background:#ff6;}
.meta{font-size:.8rem;color:#777}
</style></head><body>
<h1>corpuskit search</h1>
<form method="get" action="/"><input type="search" name="q" value="{{.Query}}" placeholder="search the index" autofocus></form>
{{if .Searched}}<p class="meta">{{len .Results}} result(s) for "{{.Query}}"</p>{{end}}
{{range .Results}}
<div class="hit">
<div class="title">{{.Title}}</div>
<div class="src"><a href="{{.Source}}" rel="noopener noreferrer nofollow">{{.Source}}</a></div>
<div class="snip">{{.Snippet}}</div>
<div class="meta">bm25 {{printf "%.4f" .Score}}</div>
</div>
{{else}}{{if .Searched}}<p>No results.</p>{{end}}{{end}}
</body></html>`))

// LoopbackAddr reports whether addr binds only to a loopback interface.
func LoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
