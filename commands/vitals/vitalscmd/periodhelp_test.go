package vitalscmd_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/vitals/errorscmd"
	"github.com/PollyGlot/google-play-cli/commands/vitals/query"
	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

var valueClause = regexp.MustCompile(`([A-Z][A-Z0-9_]+)(?: \(([^)]*)\))?`)

func advertisedValues(t *testing.T, cmd *cobra.Command, flag string, sets []vitals.MetricSet) map[string][]string {
	t.Helper()
	f := cmd.Flags().Lookup(flag)
	if f == nil {
		t.Fatalf("%s has no --%s flag", cmd.CommandPath(), flag)
	}
	_, list, ok := strings.Cut(f.Usage, ":")
	if !ok {
		t.Fatalf("%s --%s help %q has no value list", cmd.Name(), flag, f.Usage)
	}
	out := map[string][]string{}
	for _, m := range valueClause.FindAllStringSubmatch(list, -1) {
		for _, set := range sets {
			scope := m[2]
			if scope == "" || scope == "every set" || slices.Contains(strings.Split(scope, ", "), set.Name) {
				out[set.Name] = append(out[set.Name], m[1])
			}
		}
	}
	return out
}

func assertHelpMatchesValidation(t *testing.T, cmd *cobra.Command, flag string, supported func(schemaindex.Index, vitals.MetricSet) []string, sets ...vitals.MetricSet) {
	t.Helper()
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	got := advertisedValues(t, cmd, flag, sets)
	for _, set := range sets {
		want := supported(idx, set)
		if len(want) == 0 {
			t.Fatalf("%s: no supported --%s values in the index", set.Name, flag)
		}
		slices.Sort(want)
		have := slices.Sorted(slices.Values(got[set.Name]))
		if !slices.Equal(have, want) {
			t.Errorf("%s --%s help advertises %v for %s, validation accepts %v (help: %q)",
				cmd.Name(), flag, have, set.Name, want, cmd.Flags().Lookup(flag).Usage)
		}
	}
}

func TestPeriodHelp_presetsMatchValidation(t *testing.T) {
	for _, spec := range vitalscmd.Presets {
		set, _ := vitals.MetricSetByName(spec.Set)
		assertHelpMatchesValidation(t, vitalscmd.NewPresetCommand(kernel.Boot{}, spec), "period", vitals.SupportedPeriods, set)
	}
}

func TestPeriodHelp_errorCountsMatchesValidation(t *testing.T) {
	counts, _, err := errorscmd.NewCommand(kernel.Boot{}).Find([]string{"counts"})
	if err != nil {
		t.Fatalf("find errors counts: %v", err)
	}
	assertHelpMatchesValidation(t, counts, "period", vitals.SupportedPeriods, vitals.ErrorCountSet())
}

func TestPeriodHelp_queryMatchesValidationPerSet(t *testing.T) {
	assertHelpMatchesValidation(t, query.NewCommand(kernel.Boot{}), "period", vitals.SupportedPeriods, vitals.MetricSets()...)
}

func TestUserCohortHelp_queryMatchesValidationPerSet(t *testing.T) {
	assertHelpMatchesValidation(t, query.NewCommand(kernel.Boot{}), vitalscmd.UserCohortFlag, vitals.SupportedCohorts, vitals.MetricSets()...)
}

func TestUserCohortHelp_presetsMatchValidation(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	for _, spec := range vitalscmd.Presets {
		set, _ := vitals.MetricSetByName(spec.Set)
		cmd := vitalscmd.NewPresetCommand(kernel.Boot{}, spec)
		hasFlag := cmd.Flags().Lookup(vitalscmd.UserCohortFlag) != nil
		if wantFlag := len(vitals.SupportedCohorts(idx, set)) > 1; hasFlag != wantFlag {
			t.Errorf("vitals %s: --%s present = %v, want %v (the set accepts %v)", spec.Use, vitalscmd.UserCohortFlag, hasFlag, wantFlag, vitals.SupportedCohorts(idx, set))
			continue
		}
		if hasFlag {
			assertHelpMatchesValidation(t, cmd, vitalscmd.UserCohortFlag, vitals.SupportedCohorts, set)
		}
	}
}
