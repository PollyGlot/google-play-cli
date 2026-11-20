package testkit

import (
	"net/http"
	"sync/atomic"
	"time"
)

type Delayed struct {
	inner    http.RoundTripper
	delay    func(*http.Request) time.Duration
	inflight atomic.Int64
	peak     atomic.Int64
	total    atomic.Int64
}

func NewDelayed(inner http.RoundTripper, delay func(*http.Request) time.Duration) *Delayed {
	return &Delayed{inner: inner, delay: delay}
}

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

func (d *Delayed) Peak() int { return int(d.peak.Load()) }

func (d *Delayed) Requests() int { return int(d.total.Load()) }
