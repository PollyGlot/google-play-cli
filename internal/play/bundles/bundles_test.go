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

func TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP(t *testing.T) {
	dir := t.TempDir()
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
