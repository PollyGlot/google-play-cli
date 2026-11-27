package vitalscmd

import (
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

type PresetSpec struct {
	Use   string
	Short string
	Set   string
}

var byDimension = map[string]string{
	"versionCode": "versionCode",
	"device":      "deviceModel",
	"country":     "countryCode",
}

func ByChoices() string {
	keys := make([]string, 0, len(byDimension))
	for k := range byDimension {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}

func ByChoicesFor(idx schemaindex.Index, set vitals.MetricSet) string {
	supported := vitals.SupportedDimensions(idx, set)
	keys := make([]string, 0, len(byDimension))
	for k, dim := range byDimension {
		if contains(supported, dim) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func PresetParams(idx schemaindex.Index, set vitals.MetricSet, pkg, version, by, since, period, cohort string) (Params, error) {
	var dimensions []string
	if by != "" {
		dim, ok := byDimension[by]
		if !ok {
			return Params{}, exit.Usagef("unknown --by %q (valid: %s)", by, ByChoices())
		}
		if !contains(vitals.SupportedDimensions(idx, set), dim) {
			return Params{}, exit.Usagef("--by %q is not available for %s (valid: %s)", by, set.Name, ByChoicesFor(idx, set))
		}
		dimensions = []string{dim}
	}
	filter := ""
	if version != "" {
		n, err := strconv.Atoi(strings.TrimSpace(version))
		if err != nil || n <= 0 {
			return Params{}, exit.Usagef("invalid --version-code %q (want a positive versionCode, e.g. 123)", version)
		}
		filter = "versionCode = " + strconv.Itoa(n)
	}
	return Params{
		Set:        set,
		Package:    pkg,
		Dimensions: dimensions,
		Period:     period,
		Since:      since,
		Filter:     filter,
		UserCohort: cohort,
	}, nil
}

type presetInput struct {
	Package     string
	VersionCode string
	By          string
	Since       string
	Period      string
	UserCohort  string
	Describe    bool
	WindowFlags []string
}

var presetWindowFlags = []string{"since", "period", "by", "version-code", UserCohortFlag}

func runPreset(rc *kernel.RunContext, set vitals.MetricSet, in presetInput) (output.Renderable, error) {
	if err := RejectWindowFlags(in.Describe, in.WindowFlags); err != nil {
		return nil, err
	}
	if in.Describe {
		return Describe(rc, set, in.Package)
	}
	idx, err := schemaindex.Embedded()
	if err != nil {
		return nil, err
	}
	p, err := PresetParams(idx, set, in.Package, in.VersionCode, in.By, in.Since, in.Period, in.UserCohort)
	if err != nil {
		return nil, err
	}
	return Execute(rc, p)
}

func NewPresetCommand(boot kernel.Boot, spec PresetSpec) *cobra.Command {
	set, ok := vitals.MetricSetByName(spec.Set)
	if !ok {
		panic("vitalscmd: preset " + spec.Use + " references unknown metric set " + spec.Set)
	}

	var (
		outputFlag string
		in         presetInput
	)
	cmd := &cobra.Command{
		Use:   spec.Use,
		Short: spec.Short,
		Long: spec.Short + `.

An opinionated preset over ` + "`gplay vitals query " + spec.Set + "`" + `: no
metric or dimension knowledge required: the set's primary metric is reported
over the default 28-day DAILY window. The window ends at the latest time with
data (the --describe freshness), so --since counts back from there.

--by slices the timeline (` + ByChoices() + `); --version-code filters to one
versionCode. This is a READ-ONLY surface on the Play Developer Reporting
service. --output json mirrors the API response verbatim; a freshness note is
printed to stderr so an empty window is not mistaken for zero.

--describe fetches the metric set's descriptor instead of a timeline: the
latest end time for which data is available, per aggregation period, in the
set's timezone. It answers "up to when is this data complete?"; the window
flags (--since, --period, --by, --version-code) do not apply and are rejected.`,
		Example: `  gplay vitals ` + spec.Use + ` --package com.example.app

  # The last week, one row per versionCode
  gplay vitals ` + spec.Use + ` --by versionCode --since 7d

  # Up to when is the data complete?
  gplay vitals ` + spec.Use + ` --describe`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in.WindowFlags = ChangedFlags(cmd, presetWindowFlags...)
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return runPreset(rc, set, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.VersionCode, VersionCodeFlag, "", "filter to a single versionCode")
	cmd.Flags().StringVar(&in.By, "by", "", "slice the timeline by a dimension ("+ByChoices()+"; availability depends on the metric set)")
	cmd.Flags().StringVar(&in.Since, "since", DefaultSince, "window length back from now, e.g. 28d or 24h")
	cmd.Flags().StringVar(&in.Period, "period", DefaultPeriod, PeriodHelp(set))
	if help, ok := cohortHelp[set.Name]; ok {
		cmd.Flags().StringVar(&in.UserCohort, UserCohortFlag, "", help)
	}
	cmd.Flags().BoolVar(&in.Describe, DescribeFlag, false, DescribeHelp)
	RegisterVersionCodeCompletion(cmd, boot)
	return cmd
}

var Presets = []PresetSpec{
	{Use: "crashes", Short: "Crash rate for a package (vitals crashrate preset)", Set: "crashrate"},
	{Use: "anr", Short: "ANR (App Not Responding) rate for a package (vitals anrrate preset)", Set: "anrrate"},
	{Use: "slowstart", Short: "Slow start rate per start type for a package (vitals slowstartrate preset)", Set: "slowstartrate"},
	{Use: "slowrendering", Short: "Slow rendering (janky frames) rate for a package (vitals slowrenderingrate preset)", Set: "slowrenderingrate"},
	{Use: "excessivewakeup", Short: "Excessive wakeup rate for a package (vitals excessivewakeuprate preset)", Set: "excessivewakeuprate"},
	{Use: "lmk", Short: "Low-memory-kill rate for a package (vitals lmkrate preset)", Set: "lmkrate"},
	{Use: "stuckbgwakelock", Short: "Stuck background wakelock rate for a package (vitals stuckbackgroundwakelockrate preset)", Set: "stuckbackgroundwakelockrate"},
}
