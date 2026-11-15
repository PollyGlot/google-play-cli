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

// periodClause is one period named in a --period help, with the metric sets it
// is scoped to ("every set" or a comma list) when the help is per set.
var periodClause = regexp.MustCompile(`([A-Z][A-Z0-9_]+)(?: \(([^)]*)\))?`)

// advertisedPeriods reads, per metric set, which periods a --period help
// claims the command accepts. A clause without parentheses applies to every
// set in sets; "every set" too; otherwise only the sets it lists.
func advertisedPeriods(t *testing.T, cmd *cobra.Command, sets []vitals.MetricSet) map[string][]string {
	t.Helper()
	f := cmd.Flags().Lookup("period")
	if f == nil {
		t.Fatalf("%s has no --period flag", cmd.CommandPath())
	}
	_, list, ok := strings.Cut(f.Usage, ":")
	if !ok {
		t.Fatalf("%s --period help %q has no period list", cmd.Name(), f.Usage)
	}
	out := map[string][]string{}
	for _, m := range periodClause.FindAllStringSubmatch(list, -1) {
		for _, set := range sets {
			scope := m[2]
			if scope == "" || scope == "every set" || slices.Contains(strings.Split(scope, ", "), set.Name) {
				out[set.Name] = append(out[set.Name], m[1])
			}
		}
	}
	return out
}

// assertHelpMatchesValidation fails when a --period help names a period the
// validation (vitals.SupportedPeriods, what Execute checks) refuses for a set,
// or leaves out one it accepts (#781: FULL_RANGE was advertised, then exit 2).
func assertHelpMatchesValidation(t *testing.T, cmd *cobra.Command, sets ...vitals.MetricSet) {
	t.Helper()
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	got := advertisedPeriods(t, cmd, sets)
	for _, set := range sets {
		want := vitals.SupportedPeriods(idx, set)
		if len(want) == 0 {
			t.Fatalf("%s: no supported periods in the index", set.Name)
		}
		if !slices.Equal(got[set.Name], want) {
			t.Errorf("%s --period help advertises %v for %s, validation accepts %v (help: %q)",
				cmd.Name(), got[set.Name], set.Name, want, cmd.Flags().Lookup("period").Usage)
		}
	}
}

func TestPeriodHelp_presetsMatchValidation(t *testing.T) {
	for _, spec := range vitalscmd.Presets {
		set, _ := vitals.MetricSetByName(spec.Set)
		assertHelpMatchesValidation(t, vitalscmd.NewPresetCommand(kernel.Boot{}, spec), set)
	}
}

func TestPeriodHelp_errorCountsMatchesValidation(t *testing.T) {
	counts, _, err := errorscmd.NewCommand(kernel.Boot{}).Find([]string{"counts"})
	if err != nil {
		t.Fatalf("find errors counts: %v", err)
	}
	assertHelpMatchesValidation(t, counts, vitals.ErrorCountSet())
}

func TestPeriodHelp_queryMatchesValidationPerSet(t *testing.T) {
	assertHelpMatchesValidation(t, query.NewCommand(kernel.Boot{}), vitals.MetricSets()...)
}
