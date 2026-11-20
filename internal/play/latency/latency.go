package latency

import (
	"net/url"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

type Tolerance string

const (
	Sensitive Tolerance = "PRODUCT_UPDATE_LATENCY_TOLERANCE_LATENCY_SENSITIVE"
	Tolerant  Tolerance = "PRODUCT_UPDATE_LATENCY_TOLERANCE_LATENCY_TOLERANT"
)

const (
	Flag = "latency-tolerance"
	Help = "how fast Play propagates the writes: sensitive (Play's default: minutes, 7,200 updates an hour) or tolerant (up to 24 hours, 720,000 updates an hour on batch writes)"
)

var choices = map[string]Tolerance{"sensitive": Sensitive, "tolerant": Tolerant}

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

func (t Tolerance) SetQuery(q url.Values) {
	if t != "" {
		q.Set("latencyTolerance", string(t))
	}
}
