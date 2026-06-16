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

// PresetSpec describes one opinionated `gplay vitals <name>` command: a fixed
// metric set with a friendly flag surface (--by, --version) instead of the raw
// --metrics/--dimensions of `vitals query`. The presets are data, so adding the
// remaining metric sets (#260) is a list append, not new command code.
type PresetSpec struct {
	Use   string // subcommand name, e.g. "crashes"
	Short string // one-line help
	Set   string // metric-set name in the registry, e.g. "crashrate"
}

// byDimension maps the preset-friendly --by value to the API dimension name.
// The mapped dimension is still validated against the metric set's supported
// dimensions in Execute, so a set that does not support one is rejected.
var byDimension = map[string]string{
	"versionCode": "versionCode",
	"device":      "deviceModel",
	"country":     "countryCode",
}

// ByChoices is the pipe-joined list of all --by keys, for the (set-independent)
// flag help. Which keys actually apply depends on the metric set — see
// ByChoicesFor for the set-aware list used in error messages.
func ByChoices() string {
	keys := make([]string, 0, len(byDimension))
	for k := range byDimension {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}

// ByChoicesFor is the pipe-joined list of --by keys whose mapped dimension the
// given metric set actually supports (read from the snapshot). Not every set
// supports every dimension — e.g. errorCount has no countryCode — so this is
// what an actionable "--by not available" error lists.
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

// PresetParams resolves the friendly preset flags (--by, --version) into Params
// for a metric set: --by maps to the API dimension (validated against THIS set's
// supported dimensions, since not every set supports every one), --version to a
// versionCode filter. Shared by the opinionated presets and `vitals errors
// counts`. A bad --by or --version is CLI misuse; the --by error names the
// user's token (e.g. `country`), not the internal API dimension.
func PresetParams(idx schemaindex.Index, set vitals.MetricSet, pkg, version, by, since, period string) (Params, error) {
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
			return Params{}, exit.Usagef("invalid --version %q (want a positive versionCode, e.g. 123)", version)
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
	}, nil
}

// presetInput is the flag surface shared by every preset.
type presetInput struct {
	Package string
	Version string // --version: filter by versionCode
	By      string // --by: versionCode|device|country
	Since   string
	Period  string
}

// runPreset resolves the friendly preset flags into Params and delegates to
// Execute. Metrics are left empty so the set's primary metric is used — the
// whole point of a preset is that the common case needs no metric knowledge.
func runPreset(rc *kernel.RunContext, set vitals.MetricSet, in presetInput) (output.Renderable, error) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		return nil, err
	}
	p, err := PresetParams(idx, set, in.Package, in.Version, in.By, in.Since, in.Period)
	if err != nil {
		return nil, err
	}
	return Execute(rc, p)
}

// NewPresetCommand builds the cobra command for one preset. It panics at
// construction (not runtime) if spec.Set is not a registered metric set — a
// programmer error caught by any test that builds the command tree.
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
metric or dimension knowledge required — the set's primary metric is reported
over the default 28-day DAILY window.

  gplay vitals ` + spec.Use + ` --package com.example.app
  gplay vitals ` + spec.Use + ` --by versionCode --version 123
  gplay vitals ` + spec.Use + ` --since 7d --period DAILY

--by slices the timeline (` + ByChoices() + `); --version filters to one
versionCode. This is a READ-ONLY surface on the Play Developer Reporting
service. --output json mirrors the API response verbatim; a freshness note is
printed to stderr so an empty window is not mistaken for zero.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return runPreset(rc, set, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Version, "version", "", "filter to a single versionCode")
	cmd.Flags().StringVar(&in.By, "by", "", "slice the timeline by a dimension ("+ByChoices()+"; availability depends on the metric set)")
	cmd.Flags().StringVar(&in.Since, "since", DefaultSince, "window length back from now, e.g. 28d or 24h")
	cmd.Flags().StringVar(&in.Period, "period", DefaultPeriod, "aggregation period: DAILY, HOURLY, or FULL_RANGE")
	return cmd
}

// Presets is the declared list of opinionated metric-set commands — one per
// queryable metric set (the seven read-only signals). Adding a metric set to
// the registry without a preset here fails TestPresets_coverEveryMetricSet.
var Presets = []PresetSpec{
	{Use: "crashes", Short: "Crash rate for a package (vitals crashrate preset)", Set: "crashrate"},
	{Use: "anr", Short: "ANR (App Not Responding) rate for a package (vitals anrrate preset)", Set: "anrrate"},
	{Use: "slowstart", Short: "Slow cold-start rate for a package (vitals slowstartrate preset)", Set: "slowstartrate"},
	{Use: "slowrendering", Short: "Slow rendering (janky frames) rate for a package (vitals slowrenderingrate preset)", Set: "slowrenderingrate"},
	{Use: "excessivewakeup", Short: "Excessive wakeup rate for a package (vitals excessivewakeuprate preset)", Set: "excessivewakeuprate"},
	{Use: "lmk", Short: "Low-memory-kill rate for a package (vitals lmkrate preset)", Set: "lmkrate"},
	{Use: "stuckbgwakelock", Short: "Stuck background wakelock rate for a package (vitals stuckbackgroundwakelockrate preset)", Set: "stuckbackgroundwakelockrate"},
}
