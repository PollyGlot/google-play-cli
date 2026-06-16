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

func byChoices() string {
	keys := make([]string, 0, len(byDimension))
	for k := range byDimension {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
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
	var dimensions []string
	if in.By != "" {
		dim, ok := byDimension[in.By]
		if !ok {
			return nil, exit.Usagef("unknown --by %q (valid: %s)", in.By, byChoices())
		}
		dimensions = []string{dim}
	}

	filter := ""
	if in.Version != "" {
		n, err := strconv.Atoi(strings.TrimSpace(in.Version))
		if err != nil || n <= 0 {
			return nil, exit.Usagef("invalid --version %q (want a positive versionCode, e.g. 123)", in.Version)
		}
		filter = "versionCode = " + strconv.Itoa(n)
	}

	return Execute(rc, Params{
		Set:        set,
		Package:    in.Package,
		Dimensions: dimensions,
		Period:     in.Period,
		Since:      in.Since,
		Filter:     filter,
	})
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

--by slices the timeline (` + byChoices() + `); --version filters to one
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
	cmd.Flags().StringVar(&in.By, "by", "", "slice the timeline by a dimension: "+byChoices())
	cmd.Flags().StringVar(&in.Since, "since", DefaultSince, "window length back from now, e.g. 28d or 24h")
	cmd.Flags().StringVar(&in.Period, "period", DefaultPeriod, "aggregation period: DAILY, HOURLY, or FULL_RANGE")
	return cmd
}

// Presets is the declared list of opinionated metric-set commands. #259 ships
// crashes/anr; #260 appends the remaining five.
var Presets = []PresetSpec{
	{Use: "crashes", Short: "Crash rate for a package (vitals crashrate preset)", Set: "crashrate"},
	{Use: "anr", Short: "ANR (App Not Responding) rate for a package (vitals anrrate preset)", Set: "anrrate"},
}
