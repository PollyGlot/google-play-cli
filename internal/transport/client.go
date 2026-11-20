package transport

import (
	"net/http"
	"time"
)

func NewClient(rt http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{Transport: rt, Timeout: timeout}
}
