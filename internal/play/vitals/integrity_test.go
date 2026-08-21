package vitals_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// TestRegistryMatchesSnapshot anchors the metric-set registry to the embedded
// Discovery snapshot: every declared set must have its `:query` method in the
// index, and the REST Resource segment the registry uses to build the request
// URL must be the one the snapshot's flatPath carries. A hand-typo or a snapshot
// drift fails here, offline: the registry can never invent a metric set or a
// resource path the API does not have (ADR-0026).
func TestRegistryMatchesSnapshot(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	for _, set := range vitals.MetricSets() {
		m, ok := idx.Methods[set.QueryMethodID()]
		if !ok {
			t.Errorf("metric set %q: missing query method %q in index", set.Name, set.QueryMethodID())
			continue
		}
		want := "/" + set.Resource + ":query"
		if !strings.Contains(m.Path, want) {
			t.Errorf("metric set %q: index path %q does not carry resource segment %q", set.Name, m.Path, want)
		}
		// The query request must document a metrics catalog we can validate
		// against: proves SupportedMetrics has data to read.
		if len(vitals.SupportedMetrics(idx, set)) == 0 {
			t.Errorf("metric set %q: no supported metrics parsed from the index", set.Name)
		}
	}
}
