package transport

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestScopeObserver_streamsBodyWithoutReadingAhead(t *testing.T) {
	const prefix = "grant_type=jwt&scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fandroidpublisher+openid&pad="
	body := prefix + strings.Repeat("x", 256*1024)
	src := &countingReader{r: strings.NewReader(body)}

	var aheadAfterFirstKiB int
	var received string
	var innerContentLength int64
	inner := testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		innerContentLength = req.ContentLength
		first := make([]byte, 1024)
		if _, err := io.ReadFull(req.Body, first); err != nil {
			t.Fatalf("read first KiB: %v", err)
		}
		aheadAfterFirstKiB = src.n
		rest, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read rest: %v", err)
		}
		received = string(first) + string(rest)
		_ = req.Body.Close()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})

	wrapped, obs := WithScopeObserver(inner)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://oauth2.googleapis.com/token", src)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ContentLength = int64(len(body))
	resp, err := wrapped.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	if aheadAfterFirstKiB > 1024 {
		t.Errorf("source delivered %d bytes once the inner transport had read 1 KiB, want at most 1024: the observer must not buffer ahead", aheadAfterFirstKiB)
	}
	if received != body {
		t.Errorf("inner transport received %d bytes differing from the %d sent, want the body unchanged", len(received), len(body))
	}
	if innerContentLength != int64(len(body)) {
		t.Errorf("inner transport saw ContentLength = %d, want %d unchanged", innerContentLength, len(body))
	}
	if got := len(obs.captured); got > maxObservedBody {
		t.Errorf("observer retained %d bytes, want at most %d", got, maxObservedBody)
	}
	want := []string{"https://www.googleapis.com/auth/androidpublisher", "openid"}
	if got := obs.Scopes(); !slices.Equal(got, want) {
		t.Errorf("Scopes() = %q, want %q", got, want)
	}
}

func TestScopeObserver_seesTheBodyReplayedByAnInnerRetry(t *testing.T) {
	inner := &scriptRT{t: t, steps: []step{{status: 429}, {status: 200}}}
	rt, _ := newRetry(t, testkit.RoundTripFunc(inner.serve), 1)
	wrapped, obs := WithScopeObserver(rt)
	req := newReq(t, http.MethodPost, "https://oauth2.googleapis.com/token", "scope=a+b&grant_type=jwt")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := wrapped.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if inner.calls != 2 {
		t.Fatalf("attempts = %d, want 2", inner.calls)
	}
	if got, want := obs.Scopes(), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("Scopes() = %q, want %q from the replayed body", got, want)
	}
}
