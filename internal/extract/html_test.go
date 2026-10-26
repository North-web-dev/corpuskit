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

func TestHTMLStripsControlBytes(t *testing.T) {
	// A hostile page title/body carrying an ANSI escape and a BEL must come
	// out with the control bytes removed, so printing to a terminal is safe.
	doc := "<title>Evil\x1b[31mTitle\x07</title><body><p>body\x1b]0;pwned\x07text</p></body>"
	res, err := HTML(doc)
	if err != nil {
		t.Fatal(err)
	}
	// The ESC byte (which arms an ANSI sequence) must be gone; the inert
	// leftover text is harmless.
	if strings.ContainsRune(res.Title, '\x1b') || strings.ContainsRune(res.Title, '\x07') {
		t.Fatalf("title still contains control bytes: %q", res.Title)
	}
	if !strings.Contains(res.Title, "Evil") || !strings.Contains(res.Title, "Title") {
		t.Fatalf("title lost its text: %q", res.Title)
	}
	if strings.ContainsRune(res.Text, '\x1b') || strings.ContainsRune(res.Text, '\x07') {
		t.Fatalf("text still contains control bytes: %q", res.Text)
	}
}

func TestStripControl(t *testing.T) {
	in := "safe\x1b[2Jtext\twith\nnewline\x00and\x07bell"
	got := StripControl(in)
	for _, bad := range []rune{'\x1b', '\x00', '\x07'} {
		if strings.ContainsRune(got, bad) {
			t.Errorf("StripControl kept %q in %q", bad, got)
		}
	}
	if !strings.Contains(got, "\t") || !strings.Contains(got, "\n") {
		t.Errorf("StripControl dropped tab/newline: %q", got)
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
