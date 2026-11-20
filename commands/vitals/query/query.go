package query

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

type Input struct {
	MetricSet   string
	Package     string
	Metrics     []string
	Dimensions  []string
	Period      string
	Since       string
	UserCohort  string
	Describe    bool
	WindowFlags []string
}

var windowFlags = []string{"metrics", "dimensions", "period", "since", vitalscmd.UserCohortFlag}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	set, ok := vitals.MetricSetByName(in.MetricSet)
	if !ok {
		return nil, exit.Usagef("unknown metric set %q (valid: %s)", in.MetricSet, strings.Join(vitals.MetricSetNames(), ", "))
	}
	if err := vitalscmd.RejectWindowFlags(in.Describe, in.WindowFlags); err != nil {
		return nil, err
	}
	if in.Describe {
		return vitalscmd.Describe(rc, set, in.Package)
	}
	return vitalscmd.Execute(rc, vitalscmd.Params{
		Set:        set,
		Package:    in.Package,
		Metrics:    in.Metrics,
		Dimensions: in.Dimensions,
		Period:     in.Period,
		Since:      in.Since,
		UserCohort: in.UserCohort,
	})
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "query <metric-set>",
		Short: "Query a Play vitals metric set (crashrate, anrrate, …) over a window",
		Long: `Query one of the Play Developer Reporting metric sets directly: the
generic, full-coverage form the opinionated presets (vitals crashes, vitals
anr, …) wrap.

--describe fetches the metric set's descriptor instead of a timeline: the
latest end time for which data is available, per aggregation period, in the
set's timezone. It answers "up to when is this data complete?"; the window
flags (--metrics, --dimensions, --period, --since, --user-cohort) do not
apply and are rejected.

--metrics, --dimensions and --period are validated OFFLINE against the embedded
API schema; unknown values are rejected with the valid set listed (names are
never invented: they come from the snapshot). With no --metrics the set's
primary metric is used. Default window: --since 28d, --period DAILY. The
window ends at the latest time with data (the --describe freshness), so --since
counts back from there. Supported periods are per metric set: --period lists
which sets accept each one.

--user-cohort selects the user view: OS_PUBLIC (all public Android versions,
the default), APP_TESTERS (users opted in as testers) or OS_BETA (beta
Android versions only). It is checked offline per metric set like the other
flags, and OS_BETA also narrows the dimensions --dimensions accepts.

This is a READ-ONLY surface on a distinct Google service (Play Developer
Reporting), requested with the least-privilege playdeveloperreporting scope.

--output json mirrors the API response verbatim; table/markdown render the
timeline (dates × metrics, sliced by --dimensions). A freshness note is always
printed to stderr so an empty window is not mistaken for zero.`,
		Example: `  gplay vitals query crashrate --package com.example.app
  gplay vitals query crashrate --metrics crashRate,distinctUsers --dimensions versionCode
  gplay vitals query anrrate --period HOURLY --since 24h
  gplay vitals query crashrate --user-cohort OS_BETA --dimensions osBuild`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.MetricSet = args[0]
			in.WindowFlags = vitalscmd.ChangedFlags(cmd, windowFlags...)
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringSliceVar(&in.Metrics, "metrics", nil, "metrics to aggregate (default: the set's primary metric); validated against the schema")
	cmd.Flags().StringSliceVar(&in.Dimensions, "dimensions", nil, "dimensions to slice by (e.g. versionCode,countryCode); validated against the schema")
	cmd.Flags().StringVar(&in.Period, "period", vitalscmd.DefaultPeriod, vitalscmd.QueryPeriodHelp)
	cmd.Flags().StringVar(&in.Since, "since", vitalscmd.DefaultSince, "window length back from now, e.g. 28d or 24h")
	cmd.Flags().StringVar(&in.UserCohort, vitalscmd.UserCohortFlag, "", vitalscmd.QueryUserCohortHelp)
	cmd.Flags().BoolVar(&in.Describe, vitalscmd.DescribeFlag, false, vitalscmd.DescribeHelp)
	return cmd
}
