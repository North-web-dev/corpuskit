package fts

import "testing"

func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"robots", `"robots"`},
		{"rate limit", `"rate" "limit"`},
		{"  spaced   out  ", `"spaced" "out"`},
		{`"quoted phrase"`, `"quoted" "phrase"`},
		{"crawl*", `"crawl"*`},
		{"", ""},
		{`"`, ""},
		{"***", ""},
		// Hostile / malformed FTS5 syntax must be reduced to inert phrases.
		{`" OR docs MATCH "x`, `"OR" "docs" "MATCH" "x"`},
		{`NEAR(a b)`, `"NEAR(a" "b)"`},
		{`col:value`, `"col:value"`},
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("Sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
