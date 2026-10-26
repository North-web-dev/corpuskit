// Package fts turns raw user input into a safe FTS5 MATCH expression.
package fts

import "strings"

// Sanitize converts an arbitrary user string into a valid FTS5 MATCH
// expression. Every whitespace-delimited token is emitted as a quoted phrase,
// which neutralizes FTS5 operators and punctuation so hostile or malformed
// input can never break query syntax. A trailing '*' on a token is preserved
// as an FTS5 prefix query. The result is still passed as a bound parameter.
// An empty string means there is nothing to match.
func Sanitize(q string) string {
	var out []string
	for _, tok := range strings.Fields(q) {
		prefix := strings.HasSuffix(tok, "*")
		// Drop embedded double quotes; inside a phrase they are the only
		// character with meaning, so removing them makes the token inert.
		tok = strings.ReplaceAll(tok, `"`, "")
		tok = strings.TrimRight(tok, "*")
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		q := `"` + tok + `"`
		if prefix {
			q += "*"
		}
		out = append(out, q)
	}
	return strings.Join(out, " ")
}
