package anomaliescmd

import (
	"encoding/json"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

type Input struct {
	Package string
	Since   string
	Filter  string
	Limit   int
}

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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
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
	raw, truncated, err := vitals.ListAnomalies(rc.Ctx, hc, pkg, vitals.AnomalyListOptions{Filter: filter, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	anoms, err := vitals.ParseAnomalies(pkg, raw)
	if err != nil {
		return nil, err
	}
	warn(rc, len(anoms))
	if truncated {
		rc.WarnTruncated(len(anoms), "anomalies", "limit")
	}
	return Payload{Raw: raw, Anomalies: anoms}, nil
}

func warn(rc *kernel.RunContext, n int) {
	if n == 0 {
		rc.Notef("no anomalies detected in the requested window; vitals are reported with a delay, so very recent spikes may not appear yet.")
		return
	}
	rc.Notef("vitals are reported with a delay; very recent anomalies may not appear yet.")
}

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

--since builds an activeBetween(...) window for you; --filter passes a raw
AIP-160 predicate (e.g. an open-ended window) and overrides --since.

Read-only; --output json mirrors the API response verbatim; table/markdown show
the metric set, anomalous metric and value, period, and dimensions.`,
		Example: `  gplay vitals anomalies --package com.example.app
  gplay vitals anomalies --since 90d --output json

  # An open-ended window through a raw filter
  gplay vitals anomalies --filter 'activeBetween("2026-01-01T00:00:00Z", UNBOUNDED)'`,
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
	cmd.Flags().IntVar(&in.Limit, "limit", 0, "max anomalies to return (0 = all, no cap); a capped list warns on stderr")
	return cmd
}
