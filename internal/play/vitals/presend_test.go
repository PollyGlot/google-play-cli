package vitals_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestQuery_unbuildableBody_exit1_neverSent(t *testing.T) {
	set, _ := vitals.MetricSetByName("crashrate")
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{"rows":[]}`))

	_, err := vitals.Query(context.Background(), &http.Client{Transport: fake}, set, "com.example.app", []byte(`[1]`))
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
