// Package anomaliescmd implements `gplay vitals anomalies`: Play-detected metric
// anomalies (unexpected spikes in crash/ANR/etc. rates) over a window (#49).
// Read-only, on the same Play Developer Reporting service and least-privilege
// reporting scope as the rest of `vitals`.
package anomaliescmd

import (
	"encoding/json"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Since   string
	Filter  string // raw AIP-160 filter; overrides the --since-derived activeBetween
	Limit   int
}

// Payload renders detected anomalies. JSON is the verbatim API pass-through.
type Payload struct {
	Raw       json.RawMessage
	Anomalies []vitals.Anomaly
}

var columns = []output.Column[vitals.Anomaly]{
	{Key: "metric_set", Header: "METRIC_SET", Value: func(a vitals.Anomaly) string { return a.MetricSet }},
	{Key: "metric", Header: "METRIC", Value: func(a vitals.Anomaly) string { return a.Metric }},
	{Key: "value", Header: "VALUE", Value: func(a vitals.Anomaly) string { return a.Value }},
	{Key: "period", Header: "PERIOD", Value: func(a vitals.Anomaly) string { return a.Period }},
	{Key: "dimensions", Header: "DIMENSIONS", Value: func(a vitals.Anomaly) string { return a.Dimensions }},
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, columns, p.Anomalies) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Raw) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, columns, p.Anomalies) },
	}
}

// Run resolves the package, lists anomalies bounded by --filter (or a
// --since-derived activeBetween window), and renders them.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, exit.Usagef("no package: pass --package <pkg> or run gplay init in your repo")
	}

	filter := in.Filter
	if filter == "" {
		since, err := vitalscmd.ParseSince(in.Since)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		filter = vitals.ActiveBetween(now.Add(-since), now)
	}

	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	raw, err := vitals.ListAnomalies(rc.Ctx, hc, pkg, vitals.AnomalyListOptions{Filter: filter, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	anoms, err := vitals.ParseAnomalies(raw)
	if err != nil {
		return nil, err
	}
	warn(rc, len(anoms))
	return Payload{Raw: raw, Anomalies: anoms}, nil
}

// warn always notes the reporting delay on stderr so an empty result is not
// read as a hard "no anomalies, ever": very recent spikes may not be reported
// yet. Anomalies are exceptional by nature, so an empty result is the common,
// healthy case; the note just keeps the freshness caveat visible.
func warn(rc *kernel.RunContext, n int) {
	if rc.Stderr == nil {
		return
	}
	if n == 0 {
		_, _ = io.WriteString(rc.Stderr, "NOTE: no anomalies detected in the requested window; vitals are reported with a delay, so very recent spikes may not appear yet.\n")
		return
	}
	_, _ = io.WriteString(rc.Stderr, "NOTE: vitals are reported with a delay; very recent anomalies may not appear yet.\n")
}

// NewCommand returns the cobra command for `gplay vitals anomalies`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "anomalies",
		Short: "List Play-detected metric anomalies (unexpected crash/ANR spikes)",
		Long: `List the anomalies the Play Developer Reporting service has detected for a
package (unexpected spikes in crash rate, ANR rate, and the other vitals) over
a window.

  gplay vitals anomalies --package com.example.app
  gplay vitals anomalies --since 90d
  gplay vitals anomalies --filter 'activeBetween("2026-01-01T00:00:00Z", UNBOUNDED)'

--since builds an activeBetween(...) window for you; --filter passes a raw
AIP-160 predicate (e.g. an open-ended window) and overrides --since.

Read-only; --output json mirrors the API response verbatim; table/markdown show
the metric set, anomalous metric and value, period, and dimensions.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Since, "since", vitalscmd.DefaultSince, "window length back from now, e.g. 28d or 90d")
	cmd.Flags().StringVar(&in.Filter, "filter", "", "raw AIP-160 filter (overrides --since), e.g. an activeBetween(...) with UNBOUNDED")
	cmd.Flags().IntVar(&in.Limit, "limit", 0, "max anomalies to return (0 = all, no cap)")
	return cmd
}
