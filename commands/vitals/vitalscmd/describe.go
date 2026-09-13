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

// DescribeFlag is the flag name shared by every metric-set command: with it
// the command calls the set's `.get` (the descriptor) instead of its `:query`
// (the timeline) and prints data freshness per aggregation period (#545).
const DescribeFlag = "describe"

// DescribeHelp is the flag help shared by every metric-set command.
const DescribeHelp = "describe the metric set instead of querying it: latest available end time per aggregation period (window flags do not apply)"

// DescribePayload renders a metric-set descriptor: the verbatim `.get`
// response for the JSON pass-through (ADR-0003), plus the parsed freshness
// entries for table/markdown.
type DescribePayload struct {
	Raw         json.RawMessage
	Freshnesses []vitals.Freshness
}

var describeColumns = []output.Column[vitals.Freshness]{
	{Key: "period", Header: "PERIOD", Value: func(f vitals.Freshness) string { return f.Period }},
	{Key: "latest_end_time", Header: "LATEST_END_TIME", Value: func(f vitals.Freshness) string { return f.LatestEndTime }},
	{Key: "timezone", Header: "TIMEZONE", Value: func(f vitals.Freshness) string { return f.TimeZone }},
}

// Renderers satisfies output.Renderable.
func (p DescribePayload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, describeColumns, p.Freshnesses) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Raw) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, describeColumns, p.Freshnesses) },
	}
}

// Describe resolves the package, issues the set's `.get` and returns the
// DescribePayload. It is the `--describe` body shared by `vitals query`, the
// presets and `vitals errors counts`, the way Execute is shared for `:query`.
// No freshness note goes to stderr here: the freshness IS the output.
func Describe(rc *kernel.RunContext, set vitals.MetricSet, pkg string) (output.Renderable, error) {
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, exit.Usagef("no package: pass --package <pkg> or run gplay init in your repo")
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	raw, err := vitals.Describe(rc.Ctx, httpClient, set, pkg)
	if err != nil {
		return nil, err
	}
	fresh, err := vitals.ParseFreshness(raw)
	if err != nil {
		return nil, err
	}
	return DescribePayload{Raw: raw, Freshnesses: fresh}, nil
}

// ChangedFlags returns, among names, the flags the user set on the command
// line (cobra's Changed state, so a flag left at its default such as --since
// 28d is not listed). The cobra layer records it into the command's input and
// the Run layer decides: that keeps the rejection inside kernel.Run, where the
// --output json error envelope is emitted (ADR-0023), rather than short-
// circuiting before it.
func ChangedFlags(cmd *cobra.Command, names ...string) []string {
	var out []string
	for _, f := range names {
		if cmd.Flags().Changed(f) {
			out = append(out, f)
		}
	}
	return out
}

// RejectWindowFlags is the guard behind `--describe`: the descriptor has no
// window, metrics or slicing, so a query-shaping flag set alongside --describe
// is CLI misuse, named explicitly rather than silently ignored. changed is the
// ChangedFlags list of the command's window flags; nil when --describe is off.
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

// plural picks the verb form for a count, so the error reads as a sentence.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
