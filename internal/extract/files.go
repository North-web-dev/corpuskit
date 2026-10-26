package extract

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

// FileResult is the extracted content of a local file.
type FileResult struct {
	Title     string
	Text      string
	BestOnly  bool   // true when extraction is known-incomplete (e.g. PDF)
	Extractor string // which path produced the text
}

// SupportedExt reports whether the file extension is one ingest handles.
func SupportedExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".markdown", ".html", ".htm", ".pdf":
		return true
	}
	return false
}

// File reads and extracts text from a local file by extension. .txt/.md are
// taken verbatim, .html is run through the readability heuristic, and .pdf is
// a best-effort text extraction (flagged via BestOnly).
func File(path string) (FileResult, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".md", ".markdown":
		b, err := os.ReadFile(path)
		if err != nil {
			return FileResult{}, err
		}
		return FileResult{Title: titleFromText(string(b), path), Text: normalizeSpace(string(b)), Extractor: "text"}, nil
	case ".html", ".htm":
		b, err := os.ReadFile(path)
		if err != nil {
			return FileResult{}, err
		}
		r, err := HTML(string(b))
		if err != nil {
			return FileResult{}, err
		}
		title := r.Title
		if title == "" {
			title = filepath.Base(path)
		}
		return FileResult{Title: title, Text: r.Text, Extractor: "html"}, nil
	case ".pdf":
		return pdfFile(path)
	default:
		return FileResult{}, fmt.Errorf("unsupported file type %q", ext)
	}
}

// pdfFile extracts plain text from a PDF. The underlying library can panic on
// malformed input, so the extraction is wrapped in a recover; a failure
// returns a best-effort empty result marked incomplete rather than crashing.
func pdfFile(path string) (res FileResult, err error) {
	res.Extractor = "pdf"
	res.BestOnly = true
	res.Title = filepath.Base(path)
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pdf extraction failed: %v", r)
		}
	}()
	f, r, oerr := pdf.Open(path)
	if oerr != nil {
		return res, oerr
	}
	defer f.Close()
	rd, terr := r.GetPlainText()
	if terr != nil {
		return res, terr
	}
	var buf bytes.Buffer
	if _, cerr := io.Copy(&buf, rd); cerr != nil {
		return res, cerr
	}
	res.Text = normalizeSpace(buf.String())
	return res, nil
}

func titleFromText(s, path string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(strings.TrimLeft(ln, "# "))
		if ln != "" {
			if len(ln) > 120 {
				ln = ln[:120]
			}
			return ln
		}
	}
	return filepath.Base(path)
}
