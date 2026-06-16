// Package bundles_test exercises the play-layer AAB upload against a fake
// transport. Upload wraps the Android Publisher edits.bundles.upload
// endpoint: a simple-media POST of the bundle into an already-open Edit.
package bundles_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/bundles"
)

// rt is a minimal RoundTripper that records the request line and returns a
// canned bundles.upload response. A test that expects no HTTP asserts
// gotMethod stayed "".
type rt struct {
	status int
	body   string

	gotMethod string
	gotPath   string
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.gotMethod = req.Method
	r.gotPath = req.URL.Path
	status := r.status
	if status == 0 {
		status = 200
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
	}, nil
}

// TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP asserts a
// non-regular path (a directory) is rejected as a client-side validation
// error (exit 20) BEFORE any HTTP — os.Open+Stat both succeed on a
// directory, so without an explicit regular-file guard it would only fail
// later as a transport error (exit 50). Mirrors the mappings.Upload guard.
func TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP(t *testing.T) {
	dir := t.TempDir() // a directory, not a regular file
	transport := &rt{}
	hc := &http.Client{Transport: transport}

	_, err := bundles.Upload(context.Background(), hc, "com.example.app", "edit-1", dir)
	if err == nil {
		t.Fatal("Upload accepted a directory as an AAB path")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
	var ioErr *bundles.LocalIOError
	if !errors.As(err, &ioErr) {
		t.Errorf("error %v is not *bundles.LocalIOError", err)
	}
	if transport.gotMethod != "" {
		t.Errorf("uploader hit the network for a directory path: %s %s", transport.gotMethod, transport.gotPath)
	}
}
