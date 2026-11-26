package vitalscmd

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

const DescribeFlag = "describe"

const DescribeHelp = "describe the metric set instead of querying it: latest available end time per aggregation period (window flags do not apply)"

type DescribePayload struct {
	Raw         json.RawMessage
	Freshnesses []vitals.Freshness
}

var describeColumns = []output.Column[vitals.Freshness]{
	{Key: "period", Header: "PERIOD", Value: func(f vitals.Freshness) string { return f.Period }},
	{Key: "latest_end_time", Header: "LATEST_END_TIME", Value: func(f vitals.Freshness) string { return f.LatestEndTime }},
	{Key: "timezone", Header: "TIMEZONE", Value: func(f vitals.Freshness) string { return f.TimeZone }},
}

func (p DescribePayload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, describeColumns, p.Freshnesses) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Raw) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, describeColumns, p.Freshnesses) },
	}
}

func Describe(rc *kernel.RunContext, set vitals.MetricSet, flag string) (output.Renderable, error) {
	pkg, err := rc.Package(flag)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	raw, err := vitals.Describe(rc.Ctx, httpClient, set, pkg)
	if err != nil {
		return nil, err
	}
	fresh, err := vitals.ParseFreshness(pkg, raw)
	if err != nil {
		return nil, err
	}
	return DescribePayload{Raw: raw, Freshnesses: fresh}, nil
}

func ChangedFlags(cmd *cobra.Command, names ...string) []string {
	var out []string
	for _, f := range names {
		if cmd.Flags().Changed(f) {
			out = append(out, f)
		}
	}
	return out
}

func RejectWindowFlags(describe bool, changed []string) error {
	if !describe || len(changed) == 0 {
		return nil
	}
	flags := make([]string, len(changed))
	for i, f := range changed {
		flags[i] = "--" + f
	}
	return exit.Usagef("--%s describes the metric set (no window, metrics or slicing): %s %s not apply",
		DescribeFlag, strings.Join(flags, ", "), plural(len(flags), "does", "do"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
