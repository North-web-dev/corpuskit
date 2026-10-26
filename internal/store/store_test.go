package store

import (
	"context"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUpsertAndCount(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	created, err := st.Upsert(ctx, Document{Source: "http://x/a", Title: "A", Text: "robots and rate limits"})
	if err != nil || !created {
		t.Fatalf("first upsert created=%v err=%v", created, err)
	}
	created, err = st.Upsert(ctx, Document{Source: "http://x/a", Title: "A2", Text: "updated text"})
	if err != nil || created {
		t.Fatalf("second upsert created=%v err=%v (want update)", created, err)
	}
	n, err := st.Count(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Count = %d, %v; want 1", n, err)
	}
	// Updated text must be searchable; old text must not.
	res, _ := st.Search(ctx, "updated", 10)
	if len(res) != 1 {
		t.Fatalf("search updated: got %d results", len(res))
	}
	if got, _ := st.Search(ctx, "robots", 10); len(got) != 0 {
		t.Fatalf("stale term still indexed: %d results", len(got))
	}
}

func TestEmptySource(t *testing.T) {
	if _, err := newStore(t).Upsert(context.Background(), Document{Text: "x"}); err == nil {
		t.Fatal("expected error for empty source")
	}
}

func TestBM25Ranking(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	docs := []Document{
		{Source: "d1", Title: "polite crawler", Text: "crawler crawler crawler respects robots"},
		{Source: "d2", Title: "intro", Text: "a crawler is mentioned once here"},
		{Source: "d3", Title: "unrelated", Text: "nothing about the topic at all"},
	}
	for _, d := range docs {
		if _, err := st.Upsert(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	res, err := st.Search(ctx, "crawler", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2", len(res))
	}
	// d1 mentions the term far more often, so BM25 must rank it first.
	if res[0].Source != "d1" {
		t.Fatalf("ranking: got %q first, want d1", res[0].Source)
	}
	if res[0].Score > res[1].Score {
		t.Fatalf("scores not ascending: %f then %f", res[0].Score, res[1].Score)
	}
	if !strings.Contains(res[0].Snippet, "[crawler]") {
		t.Fatalf("snippet missing highlight: %q", res[0].Snippet)
	}
}

func TestHostileQueryNoCrash(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	st.Upsert(ctx, Document{Source: "d1", Title: "t", Text: "normal content"})
	hostile := []string{
		`" OR 1=1 --`,
		`NEAR(a b) AND *`,
		`documents_fts MATCH "x"`,
		`)))(((`,
		`""""`,
		`*`,
		`'; DROP TABLE documents; --`,
	}
	for _, q := range hostile {
		if _, err := st.Search(ctx, q, 10); err != nil {
			t.Fatalf("hostile query %q errored: %v", q, err)
		}
	}
}

func TestPrefixSearch(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	st.Upsert(ctx, Document{Source: "d1", Title: "t", Text: "crawling the web"})
	res, err := st.Search(ctx, "crawl*", 10)
	if err != nil {
		t.Fatalf("prefix query errored: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("prefix query got %d results, want 1", len(res))
	}
}
