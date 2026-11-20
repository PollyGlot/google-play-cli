package vitalscmd

import "github.com/PollyGlot/google-play-cli/internal/play/vitals"

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

func PeriodHelp(set vitals.MetricSet) string {
	return periodHelp[set.Name]
}

const QueryPeriodHelp = "aggregation period, per metric set: DAILY (every set) or HOURLY (crashrate, anrrate)"

const UserCohortFlag = "user-cohort"

var cohortHelp = map[string]string{
	"crashrate": "user view: OS_PUBLIC, APP_TESTERS or OS_BETA",
	"anrrate":   "user view: OS_PUBLIC, APP_TESTERS or OS_BETA",
	"lmkrate":   "user view: OS_PUBLIC, APP_TESTERS or OS_BETA",
}

const QueryUserCohortHelp = "user view, per metric set: OS_PUBLIC (every set), APP_TESTERS (crashrate, anrrate, lmkrate), OS_BETA (crashrate, anrrate, lmkrate)"
