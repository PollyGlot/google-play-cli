package transport

import (
	"net/http"
	"time"
)

// NewClient returns an http.Client over rt with timeout bounding each request
// (0: no bound). It is the one place shipped code constructs a client (the
// http-client ratchet in internal/ratchet forbids it anywhere else): the
// RunContext builds its clients through it and hands them to commands, so a
// transport concern (a deadline, --retry) is added here once rather than in
// every caller that would otherwise copy a bare client.
func NewClient(rt http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{Transport: rt, Timeout: timeout}
}
