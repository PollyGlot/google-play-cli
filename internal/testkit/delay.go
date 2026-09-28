package testkit

import (
	"net/http"
	"sync/atomic"
	"time"
)

// Delayed wraps a RoundTripper with a per-request latency and records how many
// requests were in flight at once. A concurrent command is tested through it:
// the latency makes responses complete out of request order (so an output
// that follows completion order shows up), and Peak proves the concurrency
// bound the command promises.
type Delayed struct {
	inner    http.RoundTripper
	delay    func(*http.Request) time.Duration
	inflight atomic.Int64
	peak     atomic.Int64
	total    atomic.Int64
}

// NewDelayed returns a Delayed that sleeps delay(req) before handing req to
// inner. The sleep happens outside inner, so a mutex-guarded fake does not
// serialize the latency away.
func NewDelayed(inner http.RoundTripper, delay func(*http.Request) time.Duration) *Delayed {
	return &Delayed{inner: inner, delay: delay}
}

// RoundTrip implements http.RoundTripper.
func (d *Delayed) RoundTrip(req *http.Request) (*http.Response, error) {
	d.total.Add(1)
	cur := d.inflight.Add(1)
	defer d.inflight.Add(-1)
	for {
		p := d.peak.Load()
		if cur <= p || d.peak.CompareAndSwap(p, cur) {
			break
		}
	}
	time.Sleep(d.delay(req))
	return d.inner.RoundTrip(req)
}

// Peak is the largest number of requests seen in flight at the same time.
func (d *Delayed) Peak() int { return int(d.peak.Load()) }

// Requests is how many requests went through, token exchanges included.
func (d *Delayed) Requests() int { return int(d.total.Load()) }
