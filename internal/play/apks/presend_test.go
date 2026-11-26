package apks_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/apks"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestUpload_unbuildableURL_exit1_neverSent(t *testing.T) {
	apk := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(apk, []byte("apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{"versionCode":1}`))

	_, err := apks.Upload(context.Background(), &http.Client{Transport: fake}, "", "edit-1", apk)
	if got := exit.For(err); got != 1 {
		t.Errorf("exit.For(err) = %d, want 1; err=%v", got, err)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || !api.NeverSent(apiErr.Cause) {
		t.Errorf("api.NeverSent(cause of %v) = false, want true", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("a request that cannot be built reached the network: %+v", calls)
	}
}
