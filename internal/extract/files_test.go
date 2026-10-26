package extract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFileText(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "note.txt", "plain text body\nsecond line")
	res, err := File(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "plain text body" {
		t.Errorf("title = %q", res.Title)
	}
	if !strings.Contains(res.Text, "second line") {
		t.Errorf("text = %q", res.Text)
	}
}

func TestFileMarkdown(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "doc.md", "# Title Here\n\nMarkdown paragraph content.")
	res, err := File(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Title Here" {
		t.Errorf("title = %q", res.Title)
	}
	if !strings.Contains(res.Text, "Markdown paragraph content.") {
		t.Errorf("text = %q", res.Text)
	}
}

func TestFileHTML(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "page.html", `<html><head><title>HTML Doc</title></head><body><script>x</script><p>Visible text.</p></body></html>`)
	res, err := File(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "HTML Doc" {
		t.Errorf("title = %q", res.Title)
	}
	if !strings.Contains(res.Text, "Visible text.") || strings.Contains(res.Text, "x") && strings.Contains(res.Text, "script") {
		t.Errorf("text = %q", res.Text)
	}
}

func TestSupportedExt(t *testing.T) {
	for _, ok := range []string{"a.txt", "a.md", "a.html", "a.htm", "a.pdf", "A.PDF"} {
		if !SupportedExt(ok) {
			t.Errorf("%q should be supported", ok)
		}
	}
	for _, no := range []string{"a.png", "a.docx", "a"} {
		if SupportedExt(no) {
			t.Errorf("%q should not be supported", no)
		}
	}
}

func TestUnsupportedFile(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "x.png", "binary")
	if _, err := File(p); err == nil {
		t.Fatal("expected error for unsupported type")
	}
}
