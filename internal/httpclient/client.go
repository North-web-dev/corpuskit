// Package httpclient builds the fetch client used by the crawler: a plain,
// honestly-identified net/http client by default, or a browser-shaped client
// (real browser TLS fingerprint) via impersonate-http when requested.
package httpclient

import (
	"net/http"
	"time"

	impersonate "github.com/North-web-dev/impersonate-http"
)

// New returns an HTTP client. When browser is false it is a plain net/http
// client that identifies itself honestly via the User-Agent the caller sets
// per request. When browser is true it is an impersonate-http client whose TLS
// handshake matches Chrome, for hosts that block bare Go clients. redirect is
// installed as the client's CheckRedirect policy in both cases.
func New(browser bool, timeout time.Duration, redirect func(*http.Request, []*http.Request) error) *http.Client {
	if browser {
		c := impersonate.New(impersonate.Chrome, impersonate.WithTimeout(timeout))
		c.CheckRedirect = redirect
		return c
	}
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: redirect,
	}
}
