package bundles_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/bundles"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestUpload_unbuildableURL_exit1_neverSent(t *testing.T) {
	aab := filepath.Join(t.TempDir(), "app.aab")
	if err := os.WriteFile(aab, []byte("aab"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{"versionCode":1}`))

	_, err := bundles.Upload(context.Background(), &http.Client{Transport: fake}, "", "edit-1", aab)
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
