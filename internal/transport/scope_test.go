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

	var aheadAfterFirstKiB, received int
	inner := testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		first := make([]byte, 1024)
		if _, err := io.ReadFull(req.Body, first); err != nil {
			t.Fatalf("read first KiB: %v", err)
		}
		aheadAfterFirstKiB = src.n
		rest, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read rest: %v", err)
		}
		received = len(first) + len(rest)
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
	if received != len(body) {
		t.Errorf("inner transport received %d bytes, want all %d", received, len(body))
	}
	if req.ContentLength != int64(len(body)) {
		t.Errorf("ContentLength = %d, want %d unchanged", req.ContentLength, len(body))
	}
	want := []string{"https://www.googleapis.com/auth/androidpublisher", "openid"}
	if got := obs.Scopes(); !slices.Equal(got, want) {
		t.Errorf("Scopes() = %q, want %q", got, want)
	}
}
