// Command corpuskit builds and searches a full-text index (SQLite FTS5 + BM25)
// over a website or a folder of documents. Every indexed document keeps its
// source URL or file path, so search results always link back to the original.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/North-web-dev/corpuskit/internal/crawl"
	"github.com/North-web-dev/corpuskit/internal/extract"
	"github.com/North-web-dev/corpuskit/internal/serve"
	"github.com/North-web-dev/corpuskit/internal/store"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "crawl":
		err = cmdCrawl(ctx, os.Args[2:])
	case "ingest":
		err = cmdIngest(ctx, os.Args[2:])
	case "search":
		err = cmdSearch(ctx, os.Args[2:])
	case "serve":
		err = cmdServe(ctx, os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("corpuskit", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `corpuskit `+version+` — polite full-text search index (SQLite FTS5 + BM25)

Usage:
  corpuskit crawl <start-url> [flags]   politely crawl a site into the index
  corpuskit ingest <dir> [flags]        index local .txt/.md/.html/.pdf files
  corpuskit search <query> [flags]      BM25-ranked full-text search
  corpuskit serve [flags]               read-only HTTP JSON search API + page
  corpuskit version

Every indexed document stores its source URL or file path; results link back.
Run a subcommand with -h for its flags.
`)
}

func openStore(path string) (*store.Store, error) {
	if path == "" {
		path = "corpuskit.db"
	}
	return store.Open(path)
}

func cmdCrawl(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("crawl", flag.ExitOnError)
	db := fs.String("db", "corpuskit.db", "path to the SQLite index")
	maxPages := fs.Int("max-pages", 100, "maximum pages to store")
	maxDepth := fs.Int("max-depth", 3, "maximum link depth from the start URL")
	rate := fs.Float64("rate", 1.0, "requests per second (per host)")
	conc := fs.Int("concurrency", 2, "number of concurrent fetchers")
	ua := fs.String("user-agent", crawl.DefaultUserAgent, "User-Agent header")
	browser := fs.Bool("browser", false, "use a browser-shaped TLS fingerprint (impersonate-http)")
	ignoreRobots := fs.Bool("ignore-robots", false, "do NOT honor robots.txt (impolite; off by default)")
	maxBody := fs.Int64("max-body", 5<<20, "maximum response body bytes per page")
	timeout := fs.Duration("timeout", 30*time.Second, "per-request timeout")
	quiet := fs.Bool("quiet", false, "suppress per-page progress logs")
	fs.Parse(permute(args, map[string]bool{"browser": true, "ignore-robots": true, "quiet": true}))

	if fs.NArg() != 1 {
		return errors.New("crawl requires exactly one <start-url>")
	}
	st, err := openStore(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	if *quiet {
		logf = func(string, ...any) {}
	}
	stats, err := crawl.Crawl(ctx, fs.Arg(0), st, crawl.Options{
		MaxPages: *maxPages, MaxDepth: *maxDepth, Rate: *rate, Concurrency: *conc,
		UserAgent: *ua, Browser: *browser, IgnoreRobots: *ignoreRobots,
		MaxBodyBytes: *maxBody, Timeout: *timeout, Logf: logf,
	})
	if err != nil {
		return err
	}
	total, _ := st.Count(ctx)
	fmt.Printf("crawl done: stored %d, fetched %d, skipped %d; index now holds %d documents (%s)\n",
		stats.Stored, stats.Fetched, stats.Skipped, total, *db)
	return nil
}

func cmdIngest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	db := fs.String("db", "corpuskit.db", "path to the SQLite index")
	quiet := fs.Bool("quiet", false, "suppress per-file progress logs")
	fs.Parse(permute(args, map[string]bool{"quiet": true}))
	if fs.NArg() != 1 {
		return errors.New("ingest requires exactly one <dir>")
	}
	root := fs.Arg(0)
	st, err := openStore(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	var indexed, skipped, failed int
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !extract.SupportedExt(path) {
			return nil
		}
		abs, aerr := filepath.Abs(path)
		if aerr != nil {
			abs = path
		}
		res, ferr := extract.File(path)
		if ferr != nil {
			failed++
			if !*quiet {
				fmt.Fprintf(os.Stderr, "failed: %s: %v\n", path, ferr)
			}
			return nil
		}
		if res.Text == "" {
			skipped++
			if !*quiet {
				fmt.Fprintf(os.Stderr, "skip (no text): %s\n", path)
			}
			return nil
		}
		if _, err := st.Upsert(ctx, store.Document{Source: abs, Title: res.Title, Text: res.Text}); err != nil {
			failed++
			return nil
		}
		indexed++
		if !*quiet {
			note := ""
			if res.BestOnly {
				note = " (best-effort extraction)"
			}
			fmt.Fprintf(os.Stderr, "indexed [%s]: %s%s\n", res.Extractor, path, note)
		}
		return nil
	})
	if err != nil {
		return err
	}
	total, _ := st.Count(ctx)
	fmt.Printf("ingest done: indexed %d, skipped %d, failed %d; index now holds %d documents (%s)\n",
		indexed, skipped, failed, total, *db)
	return nil
}

func cmdSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	db := fs.String("db", "corpuskit.db", "path to the SQLite index")
	limit := fs.Int("limit", 10, "maximum results")
	asJSON := fs.Bool("json", false, "emit results as JSON")
	fs.Parse(permute(args, map[string]bool{"json": true}))
	if fs.NArg() == 0 {
		return errors.New("search requires a <query>")
	}
	query := joinArgs(fs.Args())
	st, err := openStore(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	results, err := st.Search(ctx, query, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		out := make([]map[string]any, 0, len(results))
		for _, r := range results {
			out = append(out, map[string]any{
				"source": r.Source, "title": r.Title, "snippet": r.Snippet, "score": r.Score,
			})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if len(results) == 0 {
		fmt.Printf("no results for %q\n", query)
		return nil
	}
	for i, r := range results {
		title := r.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Printf("%d. %s  [bm25 %.4f]\n   %s\n   %s\n\n", i+1, title, r.Score, r.Source, r.Snippet)
	}
	return nil
}

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	db := fs.String("db", "corpuskit.db", "path to the SQLite index")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address (keep on loopback; it is unauthenticated)")
	fs.Parse(permute(args, nil))
	st, err := openStore(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	if !serve.LoopbackAddr(*addr) {
		fmt.Fprintf(os.Stderr, "WARNING: binding %s is not loopback. This API is read-only and UNAUTHENTICATED; put it behind a trusted proxy.\n", *addr)
	}
	srv := &http.Server{Addr: *addr, Handler: serve.New(st), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sc)
	}()
	fmt.Printf("corpuskit serve: read-only search on http://%s  (Ctrl-C to stop)\n", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// permute reorders args so that flags may appear before or after positional
// arguments (Go's flag package otherwise stops at the first positional).
// boolFlags names the flags that take no value, so their following token is
// not mistaken for a value.
func permute(args []string, boolFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if j := strings.IndexByte(name, '='); j >= 0 {
				name = name[:j]
				continue
			}
			if !boolFlags[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

// joinArgs reconstructs a query from argv, so `search foo bar` and
// `search "foo bar"` behave the same.
func joinArgs(args []string) string {
	s := ""
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += a
	}
	return s
}
