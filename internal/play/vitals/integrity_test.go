package vitals_test

import (
	"slices"
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

// TestMaxSampleReports_matchesDiscovery anchors the --sample-reports bound to
// the sentence it rests on, so a refresh that raises Play's cap fails here.
func TestMaxSampleReports_matchesDiscovery(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	for _, p := range idx.Methods["playdeveloperreporting.vitals.errors.issues.search"].Parameters {
		if p.Name != "sampleErrorReportLimit" {
			continue
		}
		if vitals.MaxSampleReports != 1 || !strings.Contains(p.Description, "currently only 0 and 1 are supported") {
			t.Errorf("MaxSampleReports = %d against Discovery %q: review the bound", vitals.MaxSampleReports, p.Description)
		}
		return
	}
	t.Fatal("errors.issues.search has no sampleErrorReportLimit parameter in the index")
}

func TestSupportedCohorts_readPerSetFromTheProse(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	all := []string{"OS_PUBLIC", "OS_BETA", "APP_TESTERS"}
	want := map[string][]string{
		"crashrate": all, "anrrate": all, "lmkrate": all,
		"slowstartrate": {"OS_PUBLIC"}, "slowrenderingrate": {"OS_PUBLIC"}, "excessivewakeuprate": {"OS_PUBLIC"},
		"stuckbackgroundwakelockrate": {"OS_PUBLIC"}, "anonrssandswapmemoryusage": {"OS_PUBLIC"}, "bitmapmemoryusage": {"OS_PUBLIC"},
	}
	for _, set := range vitals.MetricSets() {
		if got := vitals.SupportedCohorts(idx, set); !slices.Equal(got, want[set.Name]) {
			t.Errorf("SupportedCohorts(%s) = %v, want %v", set.Name, got, want[set.Name])
		}
	}
	if got := vitals.SupportedCohorts(idx, vitals.ErrorCountSet()); got != nil {
		t.Errorf("SupportedCohorts(errors.counts) = %v, want none: its request has no userCohort", got)
	}
}

func TestCohortDimensions_osBetaReplacesTheCatalog(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	crash, _ := vitals.MetricSetByName("crashrate")
	if got := vitals.CohortDimensions(idx, crash, "OS_BETA"); !slices.Equal(got, []string{"versionCode", "osBuild"}) {
		t.Errorf("CohortDimensions(crashrate, OS_BETA) = %v, want [versionCode osBuild]", got)
	}
	for _, cohort := range []string{"OS_PUBLIC", "APP_TESTERS"} {
		if got := vitals.CohortDimensions(idx, crash, cohort); got != nil {
			t.Errorf("CohortDimensions(crashrate, %s) = %v, want nil (the set's own catalog)", cohort, got)
		}
	}
}
