// Package extract pulls readable text out of HTML and local document files.
package extract

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// nonContent holds elements whose text is chrome, not article content.
var nonContent = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Nav: true,
	atom.Header: true, atom.Footer: true, atom.Aside: true,
	atom.Noscript: true, atom.Template: true, atom.Form: true,
	atom.Button: true, atom.Svg: true,
}

// HTMLResult is the outcome of parsing one HTML document.
type HTMLResult struct {
	Title string
	Text  string
	Links []string // raw href values, resolved and filtered by the caller
}

// HTML parses an HTML document and returns its title, a best-effort readable
// text extraction, and every href found. The extraction is a heuristic: it
// drops script/style/nav/header/footer/aside and collapses whitespace. It is
// deliberately simple and will not perfectly isolate article bodies on every
// site.
func HTML(doc string) (HTMLResult, error) {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return HTMLResult{}, err
	}
	var res HTMLResult
	var sb strings.Builder
	var walk func(n *html.Node, skip bool)
	walk = func(n *html.Node, skip bool) {
		switch n.Type {
		case html.ElementNode:
			if n.DataAtom == atom.Title && res.Title == "" {
				res.Title = strings.TrimSpace(textOf(n))
				return
			}
			if n.DataAtom == atom.A {
				for _, a := range n.Attr {
					if a.Key == "href" {
						res.Links = append(res.Links, a.Val)
					}
				}
			}
			if nonContent[n.DataAtom] {
				skip = true
			}
			if isBlock(n.DataAtom) {
				sb.WriteByte('\n')
			}
		case html.TextNode:
			if !skip {
				sb.WriteString(n.Data)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, skip)
		}
	}
	walk(root, false)
	res.Title = CleanTitle(res.Title)
	res.Text = normalizeSpace(sb.String())
	return res, nil
}

// StripControl removes control characters (including the ESC byte used for
// ANSI terminal escapes) from s, keeping only newlines and tabs. Indexed text
// is attacker-controlled third-party content, so this runs at extraction time
// to keep stored titles and bodies safe to print to a terminal or emit as JSON.
func StripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r == unicode.ReplacementChar || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// CleanTitle strips control characters and collapses all whitespace in a
// single-line title.
func CleanTitle(s string) string {
	return strings.Join(strings.Fields(StripControl(s)), " ")
}

func isBlock(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.Br, atom.Li, atom.Tr, atom.Section, atom.Article,
		atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Blockquote, atom.Pre:
		return true
	}
	return false
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// normalizeSpace collapses runs of whitespace, trimming each line and dropping
// blank lines, so the stored text is compact but keeps paragraph breaks.
func normalizeSpace(s string) string {
	lines := strings.Split(StripControl(s), "\n")
	var out []string
	for _, ln := range lines {
		if f := strings.Join(strings.Fields(ln), " "); f != "" {
			out = append(out, f)
		}
	}
	return strings.Join(out, "\n")
}
