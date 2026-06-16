// Package query implements `gplay vitals query <metric-set>`: the generic,
// read-only escape hatch over the Play Developer Reporting API metric sets
// (ADR-0026 full coverage). It is the walking skeleton the opinionated presets
// (`vitals crashes`, `vitals anr`, …) are thin wrappers over (#49).
//
// Metrics, dimensions and the aggregation period are validated OFFLINE against
// the embedded Schema index — never invented (the catalog is read from the
// snapshot). Output is the ADR-0003 JSON pass-through on stdout; table/markdown
// render the timeline (dates × metrics, sliced by the requested dimensions).
// A freshness note is always written to stderr so an empty window is never
// mistaken for "zero crashes".
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

// Input is the request-shaped struct cobra builds from flags and the
// metric-set argument.
type Input struct {
	MetricSet  string   // positional arg: crashrate, anrrate, …
	Package    string   // --package
	Metrics    []string // --metrics (empty → the set's primary metric)
	Dimensions []string // --dimensions (empty → no slicing, one row per period)
	Period     string   // --period DAILY|HOURLY|FULL_RANGE
	Since      string   // --since 28d, 24h, …
}

// Run resolves the metric set, then delegates to the shared vitals orchestration.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	set, ok := vitals.MetricSetByName(in.MetricSet)
	if !ok {
		return nil, exit.Usagef("unknown metric set %q (valid: %s)", in.MetricSet, strings.Join(vitals.MetricSetNames(), ", "))
	}
	return vitalscmd.Execute(rc, vitalscmd.Params{
		Set:        set,
		Package:    in.Package,
		Metrics:    in.Metrics,
		Dimensions: in.Dimensions,
		Period:     in.Period,
		Since:      in.Since,
	})
}

// NewCommand returns the cobra command for `gplay vitals query <metric-set>`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "query <metric-set>",
		Short: "Query a Play vitals metric set (crashrate, anrrate, …) over a window",
		Long: `Query one of the Play Developer Reporting metric sets directly — the
generic, full-coverage form the opinionated presets (vitals crashes, vitals
anr, …) wrap.

  gplay vitals query crashrate --package com.example.app
  gplay vitals query crashrate --metrics crashRate,distinctUsers --dimensions versionCode
  gplay vitals query anrrate --period HOURLY --since 24h

--metrics and --dimensions are validated OFFLINE against the embedded API
schema; unknown values are rejected with the valid set listed (names are never
invented — they come from the snapshot). With no --metrics the set's primary
metric is used. Default window: --since 28d, --period DAILY (HOURLY opt-in).

This is a READ-ONLY surface on a distinct Google service (Play Developer
Reporting), requested with the least-privilege playdeveloperreporting scope.

--output json mirrors the API response verbatim; table/markdown render the
timeline (dates × metrics, sliced by --dimensions). A freshness note is always
printed to stderr so an empty window is not mistaken for zero.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.MetricSet = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringSliceVar(&in.Metrics, "metrics", nil, "metrics to aggregate (default: the set's primary metric); validated against the schema")
	cmd.Flags().StringSliceVar(&in.Dimensions, "dimensions", nil, "dimensions to slice by (e.g. versionCode,countryCode); validated against the schema")
	cmd.Flags().StringVar(&in.Period, "period", vitalscmd.DefaultPeriod, "aggregation period: DAILY, HOURLY, or FULL_RANGE")
	cmd.Flags().StringVar(&in.Since, "since", vitalscmd.DefaultSince, "window length back from now, e.g. 28d or 24h")
	return cmd
}
