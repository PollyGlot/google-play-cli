package vitals_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestQuery_empty2xxBody_isAnEmptyPage(t *testing.T) {
	set, _ := vitals.MetricSetByName("crashrate")
	fake := testkit.NewFake(testkit.Any(http.StatusOK, ``))

	raw, err := vitals.Query(context.Background(), &http.Client{Transport: fake}, set, "com.example.app", nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if string(raw) != `{"rows":[]}` {
		t.Errorf("raw = %s, want an empty page", raw)
	}
}
