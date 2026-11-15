package vitalscmd

import "github.com/PollyGlot/google-play-cli/internal/play/vitals"

// The --period helps are written out rather than built from the embedded
// index: parsing it at every start cost each command ~3 ms for a help string.
// periodhelp_test.go fails when one of them names a period that
// vitals.SupportedPeriods, the set Execute validates against, refuses for a
// metric set, or leaves out one it accepts (#781: FULL_RANGE was advertised,
// then exit 2).

// periodHelp is the --period help of the commands bound to one metric set,
// keyed by set name.
var periodHelp = map[string]string{
	"crashrate":                   "aggregation period: DAILY or HOURLY",
	"anrrate":                     "aggregation period: DAILY or HOURLY",
	"slowstartrate":               "aggregation period: DAILY",
	"slowrenderingrate":           "aggregation period: DAILY",
	"excessivewakeuprate":         "aggregation period: DAILY",
	"lmkrate":                     "aggregation period: DAILY",
	"stuckbackgroundwakelockrate": "aggregation period: DAILY",
	"errors.counts":               "aggregation period: DAILY",
}

// PeriodHelp is the --period help of a command bound to one metric set.
func PeriodHelp(set vitals.MetricSet) string {
	return periodHelp[set.Name]
}

// QueryPeriodHelp is the --period help of `vitals query`, whose metric set is
// an argument: each period names the sets that accept it.
const QueryPeriodHelp = "aggregation period, per metric set: DAILY (every set) or HOURLY (crashrate, anrrate)"
