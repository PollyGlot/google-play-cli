// Package bundles_test exercises the play-layer AAB upload against a fake
// transport. Upload wraps the Android Publisher edits.bundles.upload
// endpoint: a simple-media POST of the bundle into an already-open Edit.
package bundles_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/bundles"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP asserts a
// non-regular path (a directory) is rejected as a client-side validation
// error (exit 20) BEFORE any HTTP: os.Open+Stat both succeed on a
// directory, so without an explicit regular-file guard it would only fail
// later as a transport error (exit 50). Mirrors the mappings.Upload guard.
func TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP(t *testing.T) {
	dir := t.TempDir() // a directory, not a regular file
	fake := testkit.NewFake(testkit.Any(200, ``))
	hc := &http.Client{Transport: fake}

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
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("uploader hit the network for a directory path: %+v", calls)
	}
}
