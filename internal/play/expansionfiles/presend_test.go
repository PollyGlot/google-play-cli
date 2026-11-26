package expansionfiles_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/expansionfiles"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestUpload_unbuildableURL_exit1_neverSent(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))

	_, err := expansionfiles.Upload(context.Background(), &http.Client{Transport: fake}, "", "edit1", 142, expansionfiles.TypeMain, writeOBB(t))
	if got := exit.For(err); got != 1 {
		t.Errorf("exit.For(err) = %d, want 1; err=%v", got, err)
	}
	if !api.NeverSent(err) {
		t.Errorf("api.NeverSent(%v) = false, want true", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("a request that cannot be built reached the network: %+v", calls)
	}
}
