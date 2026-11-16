package vitalscmd

import "github.com/PollyGlot/google-play-cli/internal/play/vitals"

// The --period helps are written out rather than built from the embedded
// index: parsing it cost every command about 2 ms at start for a help string.
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

// UserCohortFlag names the user view of a metric-set query. Its helps follow
// the --period rule above: written out, and checked by periodhelp_test.go
// against vitals.SupportedCohorts.
const UserCohortFlag = "user-cohort"

// cohortHelp is the --user-cohort help of the presets whose metric set
// accepts a view other than the default OS_PUBLIC; a preset absent here has
// no --user-cohort flag.
var cohortHelp = map[string]string{
	"crashrate": "user view: OS_PUBLIC, APP_TESTERS or OS_BETA",
	"anrrate":   "user view: OS_PUBLIC, APP_TESTERS or OS_BETA",
	"lmkrate":   "user view: OS_PUBLIC, APP_TESTERS or OS_BETA",
}

// QueryUserCohortHelp is the --user-cohort help of `vitals query`: each view
// names the sets that accept it.
const QueryUserCohortHelp = "user view, per metric set: OS_PUBLIC (every set), APP_TESTERS (crashrate, anrrate, lmkrate), OS_BETA (crashrate, anrrate, lmkrate)"
