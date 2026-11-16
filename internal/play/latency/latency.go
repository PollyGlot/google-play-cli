// Package latency carries the latencyTolerance of the Monetization catalog
// writes (#784): how fast Play propagates a product or offer update. The
// default, latency-sensitive, reaches buyers within minutes but caps an app at
// 7,200 updates an hour; latency-tolerant takes up to 24 hours and lifts the
// cap to 720,000 an hour on the batch writes. The same enum rides a query
// parameter on the patches and the one-time-product delete, and a body field
// on the state verbs and the batch requests, so internal/play/iap and
// internal/play/subscriptions share this one type.
package latency

import (
	"net/url"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// Tolerance is a ProductUpdateLatencyTolerance value. The zero value sends
// nothing, so Play applies its default.
type Tolerance string

// The values a write may carry, anchored to Discovery in internal/enumanchor.
const (
	Sensitive Tolerance = "PRODUCT_UPDATE_LATENCY_TOLERANCE_LATENCY_SENSITIVE"
	Tolerant  Tolerance = "PRODUCT_UPDATE_LATENCY_TOLERANCE_LATENCY_TOLERANT"
)

// Flag is the name of the flag iap apply and subscriptions apply share, and
// Help its usage line.
const (
	Flag = "latency-tolerance"
	Help = "how fast Play propagates the writes: sensitive (Play's default: minutes, 7,200 updates an hour) or tolerant (up to 24 hours, 720,000 updates an hour on batch writes)"
)

var choices = map[string]Tolerance{"sensitive": Sensitive, "tolerant": Tolerant}

// Parse maps a --latency-tolerance value to its Tolerance. Empty is the zero
// Tolerance; anything else outside sensitive and tolerant is CLI misuse.
func Parse(s string) (Tolerance, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return "", nil
	}
	t, ok := choices[v]
	if !ok {
		return "", exit.Usagef("invalid --%s %q (valid: sensitive, tolerant)", Flag, s)
	}
	return t, nil
}

// SetQuery adds t to q as the latencyTolerance query parameter, unless t is
// the zero Tolerance.
func (t Tolerance) SetQuery(q url.Values) {
	if t != "" {
		q.Set("latencyTolerance", string(t))
	}
}
