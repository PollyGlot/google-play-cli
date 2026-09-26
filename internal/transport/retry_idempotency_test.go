package transport

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// imagesUploadURL is edits.images.upload on its media endpoint: a POST that
// APPENDS to a gallery, the audit's example of a write a replay duplicates.
const imagesUploadURL = "https://androidpublisher.googleapis.com/upload/androidpublisher/v3/applications/com.example.app/edits/abc/listings/en-US/phoneScreenshots?uploadType=media"

// TestRetry_nonIdempotentPOSTNotReplayedAfter5xx is the #575 acceptance test:
// a 503 on a non-idempotent POST may come after Google stored the image, so
// --retry hands the failure back after a single attempt. Same for a transport
// error that is not a dial failure (a reset, a timeout mid-request).
func TestRetry_nonIdempotentPOSTNotReplayedAfter5xx(t *testing.T) {
	for _, st := range []step{{status: 503}, {status: 500}, {status: 0}} {
		inner := &scriptRT{t: t, steps: []step{st}}
		rt, delays := newRetry(t, testkit.RoundTripFunc(inner.serve), 3)
		resp, err := rt.RoundTrip(newReq(t, http.MethodPost, imagesUploadURL, "png-bytes"))
		if st.status != 0 && (err != nil || resp.StatusCode != st.status) {
			t.Fatalf("status %d: got resp=%v err=%v, want the %d handed back", st.status, resp, err, st.status)
		}
		if inner.calls != 1 || len(*delays) != 0 {
			t.Errorf("status %d: attempts=%d sleeps=%d, want exactly 1 attempt (the write may have landed)", st.status, inner.calls, len(*delays))
		}
	}
}

// TestRetry_nonIdempotentPOSTRetriedWhenNeverSent: a dial or DNS failure, or a
// 429, proves the write never ran, so even a non-idempotent POST is retried.
func TestRetry_nonIdempotentPOSTRetriedWhenNeverSent(t *testing.T) {
	cases := map[string]step{
		"dial": {err: &url.Error{Op: "Post", URL: imagesUploadURL, Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}},
		"dns":  {err: &url.Error{Op: "Post", URL: imagesUploadURL, Err: &net.DNSError{Err: "no such host", Name: "androidpublisher.googleapis.com"}}},
		"429":  {status: 429},
	}
	for name, first := range cases {
		inner := &scriptRT{t: t, steps: []step{first, {status: 200}}}
		rt, _ := newRetry(t, testkit.RoundTripFunc(inner.serve), 1)
		resp, err := rt.RoundTrip(newReq(t, http.MethodPost, imagesUploadURL, "png-bytes"))
		if err != nil || resp.StatusCode != 200 || inner.calls != 2 {
			t.Errorf("%s: resp=%v err=%v attempts=%d, want 200 after 2 attempts", name, resp, err, inner.calls)
		}
	}
}

// TestRetry_replaySafePOSTReplayed: a POST the registry declares replay-safe
// (edits.validate, read-shaped) keeps the full retry behaviour.
func TestRetry_replaySafePOSTReplayed(t *testing.T) {
	const validateURL = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/edits/abc:validate"
	inner := &scriptRT{t: t, steps: []step{{status: 503}, {status: 200}}}
	rt, _ := newRetry(t, testkit.RoundTripFunc(inner.serve), 1)
	resp, err := rt.RoundTrip(newReq(t, http.MethodPost, validateURL, ""))
	if err != nil || resp.StatusCode != 200 || inner.calls != 2 {
		t.Errorf("resp=%v err=%v attempts=%d, want 200 after 2 attempts", resp, err, inner.calls)
	}
}

// TestRetry_retryAfterClampedToMaxDelay: a day-long Retry-After, or one large
// enough to overflow time.Duration, waits the policy's MaxDelay: never a day,
// and never zero.
func TestRetry_retryAfterClampedToMaxDelay(t *testing.T) {
	for _, v := range []string{"86400", "9227000000"} {
		h := http.Header{}
		h.Set("Retry-After", v)
		inner := &scriptRT{t: t, steps: []step{{status: 429, header: h}, {status: 200}}}
		rt, delays := newRetry(t, testkit.RoundTripFunc(inner.serve), 1)
		if _, err := rt.RoundTrip(newReq(t, http.MethodGet, apiURL, "")); err != nil {
			t.Fatalf("Retry-After %s: %v", v, err)
		}
		if len(*delays) != 1 || (*delays)[0] != rt.maxDelay {
			t.Errorf("Retry-After %s: sleeps = %v, want [%v]", v, *delays, rt.maxDelay)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	const maxDelay = 30 * time.Second
	now := time.Now()
	cases := []struct {
		in     string
		want   time.Duration
		wantOK bool
		approx bool // HTTP-date: 1s resolution, and time passes during the test
	}{
		{"", 0, false, false},
		{"soon", 0, false, false},
		{"-1", 0, false, false},
		{"99999999999999999999", 0, false, false}, // not an int64, not a date
		{"0", 0, true, false},
		{"5", 5 * time.Second, true, false},
		{"30", maxDelay, true, false},
		{"31", maxDelay, true, false},
		{"86400", maxDelay, true, false},
		{"9227000000", maxDelay, true, false},
		{now.Add(-time.Hour).UTC().Format(http.TimeFormat), 0, true, false},
		{now.Add(10 * time.Second).UTC().Format(http.TimeFormat), 10 * time.Second, true, true},
		{now.Add(24 * time.Hour).UTC().Format(http.TimeFormat), maxDelay, true, false},
	}
	for _, tc := range cases {
		got, ok := parseRetryAfter(tc.in, maxDelay)
		if ok != tc.wantOK {
			t.Errorf("parseRetryAfter(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if tc.approx {
			if got < tc.want-2*time.Second || got > tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want about %v", tc.in, got, tc.want)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// FuzzParseRetryAfter holds the property the retry loop relies on: whatever a
// server sends in Retry-After, an accepted value is a delay in [0, maxDelay].
// A seconds value large enough to overflow time.Duration used to come back
// negative, which made the retry fire at once.
func FuzzParseRetryAfter(f *testing.F) {
	for _, s := range []string{"", "0", "2", "30", "86400", "9227000000", "-5", "1e9", "Sun, 06 Nov 1994 08:49:37 GMT", "Fri, 31 Dec 9999 23:59:59 GMT"} {
		f.Add(s)
	}
	const maxDelay = 30 * time.Second
	f.Fuzz(func(t *testing.T, v string) {
		d, ok := parseRetryAfter(v, maxDelay)
		if ok && (d < 0 || d > maxDelay) {
			t.Errorf("parseRetryAfter(%q) = %v, outside [0, %v]", v, d, maxDelay)
		}
		if !ok && d != 0 {
			t.Errorf("parseRetryAfter(%q) rejected the value but returned %v", v, d)
		}
	})
}
