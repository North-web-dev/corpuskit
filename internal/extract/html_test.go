package extract

import (
	"strings"
	"testing"
)

func TestHTMLExtract(t *testing.T) {
	doc := `<!doctype html><html><head><title>  My Page  </title>
	<style>.x{color:red}</style></head>
	<body>
	<nav>Home About Contact</nav>
	<script>var tracker=1;</script>
	<article><h1>Heading</h1><p>First paragraph of real content.</p>
	<p>Second paragraph with <a href="/next">a link</a> inside.</p></article>
	<footer>copyright boilerplate</footer>
	</body></html>`
	res, err := HTML(doc)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "My Page" {
		t.Errorf("title = %q", res.Title)
	}
	if !strings.Contains(res.Text, "First paragraph of real content.") {
		t.Errorf("missing content: %q", res.Text)
	}
	for _, bad := range []string{"tracker", "color:red", "Home About Contact", "copyright boilerplate"} {
		if strings.Contains(res.Text, bad) {
			t.Errorf("chrome/script leaked into text: %q in %q", bad, res.Text)
		}
	}
	if len(res.Links) != 1 || res.Links[0] != "/next" {
		t.Errorf("links = %v", res.Links)
	}
}

func TestHTMLNoTitle(t *testing.T) {
	res, err := HTML(`<html><body><p>hello world</p></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "" {
		t.Errorf("expected empty title, got %q", res.Title)
	}
	if !strings.Contains(res.Text, "hello world") {
		t.Errorf("text = %q", res.Text)
	}
}
