# corpuskit

[![Release](https://img.shields.io/github/v/release/North-web-dev/corpuskit?sort=semver)](https://github.com/North-web-dev/corpuskit/releases) [![Go Reference](https://pkg.go.dev/badge/github.com/North-web-dev/corpuskit.svg)](https://pkg.go.dev/github.com/North-web-dev/corpuskit) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE) ![Go](https://img.shields.io/github/go-mod/go-version/North-web-dev/corpuskit)

**One pure-Go binary that builds a full-text search index (SQLite FTS5 + BM25) over a website or a folder of documents — then searches it.**

corpuskit politely crawls a site or ingests a directory, extracts readable text, and stores every document **with its source URL or file path**. Search is ranked with BM25 and each result links straight back to the original. It ships as a single static binary (`CGO_ENABLED=0`, no system libraries), so it drops onto a server or into a scratch container with nothing to install.

## Polite and provenance-first, on purpose

This tool is built to be a good citizen of the web, and that is a feature, not a footnote:

- **It obeys `robots.txt`.** The crawler fetches and parses `robots.txt` before anything else and honors `Disallow` and `Crawl-delay`. Ignoring it requires an explicit `--ignore-robots` flag that defaults off and prints a warning.
- **It rate-limits.** A conservative default of 1 request/second per host, small concurrency, and a per-request body cap. If `robots.txt` asks for a larger `Crawl-delay`, that wins.
- **It stays in scope.** Same host only by default; it never follows non-`http(s)` links or redirects off the start host; URLs are normalized and any embedded credentials are stripped before a request is made or logged.
- **It keeps provenance.** Every indexed document stores its source (URL for crawled pages, absolute file path for ingested files). Search always shows where a result came from, so you — and anyone you share results with — can go read the original. Respecting copyright starts with always pointing back to the source.

If you need text that a site's terms, license, or law don't let you store or redistribute, that's on you to check — corpuskit makes the source explicit precisely so that's always possible.

## Install

```sh
go install github.com/North-web-dev/corpuskit/cmd/corpuskit@latest
```

Or grab a prebuilt static binary from the [releases page](https://github.com/North-web-dev/corpuskit/releases) (linux/darwin, amd64/arm64), or build from source:

```sh
git clone https://github.com/North-web-dev/corpuskit
cd corpuskit
CGO_ENABLED=0 go build -ldflags="-s -w" -o corpuskit ./cmd/corpuskit
```

## Subcommands

### `crawl` — politely index a website

```sh
corpuskit crawl http://example.com --db site.db --rate 1 --max-depth 3 --max-pages 100
```

Real output against a small local test site (note the `robots.txt`-disallowed page being skipped):

```
stored [d0]: http://127.0.0.1:8231/index.html (302 chars)
skip (robots): http://127.0.0.1:8231/private/secret.html
stored [d1]: http://127.0.0.1:8231/guide/ranking.html (241 chars)
stored [d1]: http://127.0.0.1:8231/guide/crawling.html (250 chars)
crawl done: stored 3, fetched 3, skipped 1; index now holds 3 documents (site.db)
```

Flags: `--db`, `--max-pages` (default 100), `--max-depth` (default 3), `--rate` req/s (default 1), `--concurrency` (default 2), `--user-agent`, `--max-body` bytes (default 5 MiB), `--timeout`, `--browser`, `--ignore-robots`, `--quiet`.

`--browser` routes fetches through [`github.com/North-web-dev/impersonate-http`](https://github.com/North-web-dev/impersonate-http), giving the requests a real browser's TLS fingerprint so a site that blocks bare Go clients still responds. It is **off by default**: the default client is a plain `net/http` client that identifies itself honestly via its `User-Agent`.

### `ingest` — index a folder of documents

```sh
corpuskit ingest ./docs --db docs.db
```

Real output over a directory holding `.md`, `.txt`, `.html`, and `.pdf`:

```
indexed [text]: docs/notes.md
indexed [html]: docs/page.html
indexed [text]: docs/readme.txt
indexed [pdf]: docs/report.pdf (best-effort extraction)
ingest done: indexed 4, skipped 0, failed 0; index now holds 4 documents (docs.db)
```

`.txt` / `.md` are indexed verbatim, `.html` runs through the same readability heuristic as the crawler, and `.pdf` is a best-effort text extraction (see **Honest limits**). The source for an ingested file is its absolute path.

### `search` — BM25-ranked full-text search

```sh
corpuskit search "bm25 ranking" --db site.db --limit 10
```

Real output (snippets wrap matched terms in `[...]`; lower BM25 = better match):

```
1. BM25 Ranking  [bm25 -0.8873]
   http://127.0.0.1:8231/guide/ranking.html
   [BM25] ranking
   Search results are ranked with the [BM25] algorithm from SQLite…

2. CorpusKit Demo Home  [bm25 -0.0000]
   http://127.0.0.1:8231/index.html
   …See the crawling guide, the [ranking] guide, and the internal notes.
```

`--json` emits machine-readable results:

```json
[
  {
    "score": -0.8873476606031657,
    "snippet": "[BM25] ranking\nSearch results are ranked with the [BM25] algorithm from SQLite…",
    "source": "http://127.0.0.1:8231/guide/ranking.html",
    "title": "BM25 Ranking"
  }
]
```

A trailing `*` on a term is a prefix search (`crawl*` matches *crawling*). The query is parsed into quoted phrase tokens and passed to SQLite as a bound parameter, so operator-laden or malformed input never breaks the query — it simply returns no results.

### `serve` — read-only HTTP search API + page

```sh
corpuskit serve --db site.db --addr 127.0.0.1:8080
```

`GET /search?q=...&limit=...` returns JSON:

```sh
$ curl 'http://127.0.0.1:8080/search?q=bm25&limit=1'
{"query":"bm25","count":1,"results":[{"source":"http://127.0.0.1:8231/guide/ranking.html","title":"BM25 Ranking","snippet":"[BM25] ranking\nSearch results are ranked with the [BM25] algorithm from SQLite…","score":-0.8873476606031657}]}
```

`GET /` serves a tiny self-contained search page. **This server is read-only and unauthenticated.** It binds to `127.0.0.1` by default and warns if you bind a non-loopback address — keep it on loopback or behind a trusted reverse proxy.

## Security notes

A security review was expected for this tool, so these were handled up front:

- **Loopback by default.** `serve` binds `127.0.0.1:8080`; a non-loopback bind prints a warning. The API is read-only and unauthenticated.
- **Output escaping.** Indexed content is third-party and untrusted. The search page renders every title, snippet, and source through `html/template`, which context-escapes automatically; a `<script>` in an indexed title comes out as inert, escaped text, and `javascript:` source URLs are filtered. The page also sends a restrictive `Content-Security-Policy` and `X-Content-Type-Options: nosniff`.
- **Parameterized SQL only.** All queries use bound parameters. User search input is sanitized into a safe FTS5 MATCH expression (each whitespace-delimited token becomes a quoted phrase) and bound, so hostile input such as `" OR 1=1 --` or `NEAR(a b)` cannot break FTS5 syntax or inject SQL — it returns no results instead of erroring.
- **Crawler safety.** Body size, total pages, depth, and per-host rate are all capped; only `http(s)` URLs are fetched; redirects off the start host are refused; URLs are normalized and credentials stripped from requests and logs.
- **`robots.txt` honored** unless `--ignore-robots` is explicitly set (default off, prints a warning).

## How it works

- **Pure-Go SQLite.** corpuskit uses [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite), a CGO-free SQLite. FTS5 is compiled in and confirmed working: documents live in an external-content FTS5 table kept in sync by triggers. This keeps the binary fully static (`CGO_ENABLED=0`) with no C toolchain or shared libraries, consistent with the rest of our tooling.
- **BM25 ranking.** Ranking is SQLite FTS5's built-in `bm25()`. FTS5 returns smaller (more negative) scores for better matches, and results are ordered ascending by that score. Snippets come from FTS5's `snippet()`.
- **Readability heuristic.** HTML text extraction parses the document with `golang.org/x/net/html`, drops `script`/`style`/`nav`/`header`/`footer`/`aside`/`form` and similar chrome, and collapses whitespace while preserving paragraph breaks.

## Honest limits

- **Readability is a heuristic, not magic.** It strips obvious chrome and keeps block text, but it does not run a full article-extraction model. On complex or unusual layouts it may keep some boilerplate or miss some content. It is good enough for indexing; it is not a perfect reader-mode.
- **PDF is best-effort.** Text extraction uses [`github.com/ledongthuc/pdf`](https://github.com/ledongthuc/pdf) and is marked `(best-effort extraction)` in logs. It works well on simple text PDFs (verified end-to-end) but can miss or garble text in scanned/image PDFs, complex layouts, unusual encodings, or encrypted files. There is no OCR. Extraction failures are caught (the library can panic on malformed input) and reported rather than crashing the run.
- **robots scope.** The `robots.txt` parser implements the common subset: `User-agent` groups (most-specific then `*`), `Disallow`, `Allow` (longest-match wins), and `Crawl-delay`. It does not implement wildcard path patterns (`*`, `$`) or sitemap directives. When in doubt it errs toward *not* fetching.
- **Crawl scope** is exact-host, not registered-domain: `www.example.com` and `example.com` are treated as different hosts.
- **Non-HTML pages are skipped** by the crawler (by `Content-Type`); only HTML is indexed from a crawl.

## Development

```sh
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

A `Dockerfile` (distroless, nonroot, static) and CI (build + `go test -race`) are included; tagged releases publish multi-arch static binaries.

## Support

If corpuskit saves you time, a ⭐ on the repo is appreciated — and so is a tip.

| Coin | Address |
|---|---|
| **BTC** | `bc1q0sfghv7yfl594gjurhyhlhdgm0dtwh4dkjha36` |
| **ETH / USDT-ERC20 / BNB-BEP20** | `0xB827b5a8B6c5111946c5D872933b2C23484E87dE` |
| **SOL** | `3ch5aHrPp3CierhiNXmqmtFH2dMHh6Gfv7a97KtLMMCh` |
| **USDT-TRC20 / TRX** | `TUAyz7TjgJdFVD2oFHWUUTNrzzAnBRgySG` |
| **TON** | `UQAJmYVuR3hh02etjaPKJF-h6jcpdSnq9ZIFvI64NEskS6tx` |

## License

MIT — see [LICENSE](LICENSE).
