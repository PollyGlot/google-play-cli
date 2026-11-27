package vitals_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

func TestRequiredDimensions_readFromTheResourceDescription(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"slowstartrate": "startType", "errors.counts": "reportType"}
	for _, set := range append(vitals.MetricSets(), vitals.ErrorCountSet()) {
		if got := strings.Join(vitals.RequiredDimensions(idx, set), ","); got != want[set.Name] {
			t.Errorf("RequiredDimensions(%s) = %q, want %q", set.Name, got, want[set.Name])
		}
	}
}
