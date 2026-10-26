package crawl

import (
	"testing"
	"time"
)

func TestParseRobotsGroups(t *testing.T) {
	body := `
User-agent: *
Disallow: /private/
Crawl-delay: 2

User-agent: corpuskit
Disallow: /secret/
Allow: /secret/public/
`
	r := parseRobots(body, "corpuskit")
	if !r.found {
		t.Fatal("expected a matching group")
	}
	if r.allowed("/secret/x") {
		t.Error("/secret/x should be disallowed")
	}
	if !r.allowed("/secret/public/y") {
		t.Error("/secret/public/y should be allowed (Allow overrides)")
	}
	if !r.allowed("/private/x") {
		t.Error("/private/ belongs to the * group, not ours")
	}
}

func TestParseRobotsWildcardAndDelay(t *testing.T) {
	body := "User-agent: *\nDisallow: /no/\nCrawl-delay: 1.5\n"
	r := parseRobots(body, "somebot")
	if r.allowed("/no/page") {
		t.Error("/no/page should be disallowed via * group")
	}
	if !r.allowed("/yes/page") {
		t.Error("/yes/page should be allowed")
	}
	if r.crawlDelay != 1500*time.Millisecond {
		t.Errorf("crawlDelay = %v", r.crawlDelay)
	}
}

func TestParseRobotsEmptyDisallowAllowsAll(t *testing.T) {
	r := parseRobots("User-agent: *\nDisallow:\n", "x")
	if !r.allowed("/anything") {
		t.Error("empty Disallow means allow everything")
	}
}

func TestParseRobotsNoGroup(t *testing.T) {
	r := parseRobots("# just a comment\n", "x")
	if r.found {
		t.Error("no groups should mean not found")
	}
	if !r.allowed("/x") {
		t.Error("absent rules allow all")
	}
}
