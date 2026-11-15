package vitalscmd

import (
	"slices"
	"strings"
	"sync"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// periodIndex is the embedded index, parsed once per process: every vitals
// command builds its --period help from it, and a parse per command would cost
// each one at every start.
var periodIndex = sync.OnceValue(func() schemaindex.Index {
	idx, err := schemaindex.Embedded()
	if err != nil {
		// The embedded bytes are fixed at build time and parsed by their own
		// tests: a failure here is a broken build, caught when the tree is built.
		panic("vitalscmd: parse the embedded schema index: " + err.Error())
	}
	return idx
})

// PeriodHelp is the --period help of a command bound to one metric set. It is
// built from vitals.SupportedPeriods, the set Execute validates against, so the
// help cannot advertise a period the command then refuses (#781).
func PeriodHelp(set vitals.MetricSet) string {
	return "aggregation period: " + orList(vitals.SupportedPeriods(periodIndex(), set))
}

// QueryPeriodHelp is the --period help of `vitals query`, whose metric set is
// an argument: each period names the sets that accept it, from the same
// vitals.SupportedPeriods the validation reads.
func QueryPeriodHelp() string {
	idx := periodIndex()
	sets := vitals.MetricSets()
	var periods []string
	accepting := map[string][]string{}
	for _, set := range sets {
		for _, p := range vitals.SupportedPeriods(idx, set) {
			if !slices.Contains(periods, p) {
				periods = append(periods, p)
			}
			accepting[p] = append(accepting[p], set.Name)
		}
	}
	clauses := make([]string, len(periods))
	for i, p := range periods {
		scope := strings.Join(accepting[p], ", ")
		if len(accepting[p]) == len(sets) {
			scope = "every set"
		}
		clauses[i] = p + " (" + scope + ")"
	}
	return "aggregation period, per metric set: " + orList(clauses)
}

// orList joins values as prose: "A", "A or B", "A, B or C".
func orList(values []string) string {
	if len(values) < 2 {
		return strings.Join(values, "")
	}
	return strings.Join(values[:len(values)-1], ", ") + " or " + values[len(values)-1]
}
