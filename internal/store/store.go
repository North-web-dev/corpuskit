// Package store is the SQLite + FTS5 persistence layer: it holds documents
// with their provenance (source URL or file path) and runs BM25-ranked
// full-text search over them using the pure-Go modernc.org/sqlite driver.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/North-web-dev/corpuskit/internal/fts"
	_ "modernc.org/sqlite"
)

// Document is one indexed item. Source is the provenance anchor (an http(s)
// URL for crawled pages, an absolute file path for ingested files) and is
// unique: re-indexing the same source upserts in place.
type Document struct {
	Source      string
	Title       string
	Text        string
	FetchedAt   time.Time
	ContentHash string
}

// Result is one ranked search hit. Score is the raw FTS5 BM25 value where a
// smaller (more negative) number is a better match.
type Result struct {
	Source  string
	Title   string
	Snippet string
	Score   float64
}

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS documents (
	id           INTEGER PRIMARY KEY,
	source       TEXT NOT NULL UNIQUE,
	title        TEXT NOT NULL DEFAULT '',
	text         TEXT NOT NULL DEFAULT '',
	fetched_at   TEXT NOT NULL DEFAULT '',
	content_hash TEXT NOT NULL DEFAULT ''
);
CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
	title, text,
	content='documents', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
	INSERT INTO documents_fts(rowid, title, text) VALUES (new.id, new.title, new.text);
END;
CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
	INSERT INTO documents_fts(documents_fts, rowid, title, text) VALUES ('delete', old.id, old.title, old.text);
END;
CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents BEGIN
	INSERT INTO documents_fts(documents_fts, rowid, title, text) VALUES ('delete', old.id, old.title, old.text);
	INSERT INTO documents_fts(rowid, title, text) VALUES (new.id, new.title, new.text);
END;`

// Open opens (creating if needed) the SQLite database at path and ensures the
// schema exists. Use ":memory:" for an ephemeral database.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// A single pooled connection keeps an in-memory DB coherent and avoids
	// writer contention for this mostly-serial workload.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// HashContent returns the hex SHA-256 of text, used as content_hash.
func HashContent(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Upsert inserts the document or updates the existing row with the same
// source. It reports whether the row was newly created. If FetchedAt is zero
// the current time is used; if ContentHash is empty it is derived from Text.
func (s *Store) Upsert(ctx context.Context, d Document) (created bool, err error) {
	if d.Source == "" {
		return false, errors.New("store: empty source")
	}
	if d.FetchedAt.IsZero() {
		d.FetchedAt = time.Now().UTC()
	}
	if d.ContentHash == "" {
		d.ContentHash = HashContent(d.Text)
	}
	var existed int
	_ = s.db.QueryRowContext(ctx, `SELECT 1 FROM documents WHERE source = ?`, d.Source).Scan(&existed)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO documents (source, title, text, fetched_at, content_hash)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source) DO UPDATE SET
			title=excluded.title, text=excluded.text,
			fetched_at=excluded.fetched_at, content_hash=excluded.content_hash`,
		d.Source, d.Title, d.Text, d.FetchedAt.UTC().Format(time.RFC3339), d.ContentHash)
	if err != nil {
		return false, err
	}
	return existed == 0, nil
}

// Count returns the number of indexed documents.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents`).Scan(&n)
	return n, err
}

// Search runs a BM25-ranked FTS5 MATCH for query, returning at most limit
// results best-first. The raw query is sanitized into a safe MATCH expression
// and passed as a bound parameter, so malformed input yields no results rather
// than an error.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	match := fts.Sanitize(query)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.source, d.title,
		       snippet(documents_fts, 1, '[', ']', '…', 12) AS snip,
		       bm25(documents_fts) AS score
		FROM documents_fts
		JOIN documents d ON d.id = documents_fts.rowid
		WHERE documents_fts MATCH ?
		ORDER BY score
		LIMIT ?`, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.Source, &r.Title, &r.Snippet, &r.Score); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
