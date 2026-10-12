// Package familycmd builds the `gplay reports <family>` commands from the
// family catalogue (internal/reports, ADR-0050): one command per Bulk report
// family, all with the same flags, selection rules and output. A family
// command is therefore one catalogue entry plus one registration line.
//
// The data channel is the Reporting bucket over the Cloud Storage API, with
// the read-only storage scope its registration declares (kernel.WithScope),
// like `reviews history` (ADR-0037). Ships [experimental]: the columns are
// Google's.
package familycmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/gcs"
	"github.com/PollyGlot/google-play-cli/internal/reports"
	reportbucket "github.com/PollyGlot/google-play-cli/internal/reports/bucket"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package     string
	Month       string // --month YYYY-MM; "" with no range = the latest month present
	Since       string // --since YYYY-MM, range start (with --until)
	Until       string // --until YYYY-MM, range end (with --since)
	Dimension   string // --dimension; "" = the family's default split when present
	Bucket      string // --bucket override; "" = derive from the developer-id
	DeveloperID string // --developer-id (feeds the default bucket name only)
	Columns     string // --columns; "" = the family's default table columns
}

// Payload satisfies output.Renderable: the merged report, and the columns the
// table and Markdown views draw.
type Payload struct {
	Report  reports.Report
	Columns []output.Column[reports.Row]
}

// Renderers draws the selected columns as a table or Markdown, and emits the
// ADR-0050 envelope as JSON (every column, whatever --columns says).
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Columns, p.Report.Table.Rows) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Report) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Columns, p.Report.Table.Rows) },
	}
}

// Run validates the flags (before any network call), lists the family's
// exports for the package, selects the months and the split against what the
// bucket holds, fetches and decodes each export and merges them.
func Run(rc *kernel.RunContext, f reports.Family, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	months, err := requestedMonths(in)
	if err != nil {
		return nil, err
	}
	bucket, err := reportbucket.Resolve(rc, in.Bucket, in.DeveloperID)
	if err != nil {
		return nil, err
	}
	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	listing, err := gcs.ListObjects(rc.Ctx, hc, bucket, f.ListPrefix(pkg))
	if err != nil {
		return nil, reportbucket.Classify(bucket, f.Permission, f.Page, err)
	}
	objs, skipped := f.Objects(listing, pkg)
	for _, name := range skipped {
		rc.Logf("ignoring %s: not a %s export name", name, f.Name)
	}
	sel, err := f.Select(objs, months, strings.TrimSpace(in.Dimension))
	var splitErr *reports.SplitError
	switch {
	case errors.Is(err, reports.ErrNoReport):
		return nil, noReport(f, pkg, bucket, months)
	case errors.As(err, &splitErr):
		return nil, exit.Usagef("%s", err)
	case err != nil:
		return nil, err
	}
	for _, m := range sel.Missing {
		rc.Warnf("no %s report for %s with dimension %q: skipped", f.Name, reports.DisplayMonth(m), sel.Split)
	}

	tables := make([]reports.Table, 0, len(sel.Objects))
	id := reports.Identity{Family: f.Name, Package: pkg, Split: sel.Split}
	for _, o := range sel.Objects {
		raw, err := gcs.FetchObjectLimit(rc.Ctx, hc, bucket, o.Name, f.MaxObjectBytes)
		if err != nil {
			return nil, reportbucket.Classify(bucket, f.Permission, f.Page, err)
		}
		t, err := reports.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", o.Name, err)
		}
		tables = append(tables, t)
		id.Months = append(id.Months, reports.DisplayMonth(o.Month))
		id.Objects = append(id.Objects, o.Name)
	}
	report := reports.Report{Identity: id, Table: reports.Merge(tables...)}

	cols, err := tableColumns(f, report.Table.Columns, in.Columns)
	if err != nil {
		return nil, err
	}
	return Payload{Report: report, Columns: cols}, nil
}

// requestedMonths validates the month flags: nil means "the latest month
// present", one month is --month, several a --since/--until range.
func requestedMonths(in Input) ([]string, error) {
	rangeMode := in.Since != "" || in.Until != ""
	switch {
	case rangeMode && in.Month != "":
		return nil, exit.Usagef("--month and --since/--until are mutually exclusive")
	case rangeMode && (in.Since == "" || in.Until == ""):
		return nil, exit.Usagef("--since and --until must be given together")
	case rangeMode:
		months, err := reports.MonthRange(in.Since, in.Until)
		if err != nil {
			return nil, exit.Usagef("%s", err)
		}
		return months, nil
	case in.Month != "":
		m, err := reports.NormalizeMonth(in.Month)
		if err != nil {
			return nil, exit.Usagef("%s", err)
		}
		return []string{m}, nil
	}
	return nil, nil
}

// noReport is the "nothing to read" error: the bucket answered, but holds no
// export of the family for the package (and months) asked. ADR-0050 gives it
// exit 30 like `reviews history`: the report is absent, the command line is
// well-formed (the fix is in --package, --month, --bucket, or in waiting for
// Play to publish).
func noReport(f reports.Family, pkg, bucket string, months []string) error {
	when := ""
	switch len(months) {
	case 0:
	case 1:
		when = " for " + reports.DisplayMonth(months[0])
	default:
		when = " for " + reports.DisplayMonth(months[0]) + ".." + reports.DisplayMonth(months[len(months)-1])
	}
	return &noReportError{msg: fmt.Sprintf("no %s report for %s%s in bucket %s: Play publishes a month a few days after it ends; "+
		"check --package, pick a published month (gplay reports list --family %s --package %s), "+
		"or pass --bucket if the derived name is wrong", f.Name, pkg, when, bucket, f.Name, pkg)}
}

// noReportError carries exit 30 (an absent resource), the code `reviews
// history` gives the same situation.
type noReportError struct{ msg string }

func (e *noReportError) Error() string { return e.msg }
func (e *noReportError) ExitCode() int { return 30 }

// tableColumns resolves --columns against the report's own columns. Without
// it, the table shows every column but the family's hidden ones, so a column
// Google adds is visible; all of them when nothing else is left.
func tableColumns(f reports.Family, cols []reports.Column, spec string) ([]output.Column[reports.Row], error) {
	all := make([]output.Column[reports.Row], len(cols))
	for i, c := range cols {
		key := c.Key
		header := c.Header
		if header == "" {
			header = key
		}
		all[i] = output.Column[reports.Row]{Key: key, Header: header, Value: func(r reports.Row) string { return r[key] }}
	}
	if strings.TrimSpace(spec) != "" {
		return output.NewColumnSet(all...).Resolve(spec)
	}
	hidden := make(map[string]bool, len(f.TableHidden))
	for _, k := range f.TableHidden {
		hidden[k] = true
	}
	shown := make([]output.Column[reports.Row], 0, len(all))
	for _, c := range all {
		if !hidden[c.Key] {
			shown = append(shown, c)
		}
	}
	if len(shown) == 0 {
		return all, nil
	}
	return shown, nil
}

// NewCommand returns the cobra command `gplay reports <f.Name>`.
func NewCommand(boot kernel.Boot, f reports.Family) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:     f.Name,
		Short:   f.Summary,
		Long:    long(f),
		Example: example(f),
		Args:    cobra.NoArgs,

		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, f, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Month, "month", "", "month to read as YYYY-MM (default: the latest month present in the bucket)")
	cmd.Flags().StringVar(&in.Since, "since", "", "range start as YYYY-MM (with --until; mutually exclusive with --month)")
	cmd.Flags().StringVar(&in.Until, "until", "", "range end as YYYY-MM (with --since; mutually exclusive with --month)")
	cmd.Flags().StringVar(&in.Dimension, "dimension", "", dimensionHelp(f))
	cmd.Flags().StringVar(&in.Bucket, "bucket", "", "Reporting bucket name override (default: pubsite_prod_rev_<developerId>)")
	cmd.Flags().StringVar(&in.DeveloperID, "developer-id", "", "Play Console Developer account id used to derive the bucket (overrides the active Account's, env, and project-local)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show, by JSON key (default: every column but "+strings.Join(f.TableHidden, ", ")+")")
	return cmd
}

func dimensionHelp(f reports.Family) string {
	if f.DefaultSplit == "" {
		return "dimension file to read, as the bucket names it"
	}
	return "dimension file to read, as the bucket names it (default: " + f.DefaultSplit + " when present)"
}

// long is the --help body: the family's own paragraph, then the text every
// family shares, filled from its catalogue entry.
func long(f reports.Family) string {
	return f.About + `

The export is read from the Reporting bucket over the Cloud Storage API with
a read-only scope (devstorage.read_only); the service account needs the
"` + string(f.Permission) + `" (global) permission in the Play Console. The bucket
defaults to pubsite_prod_rev_<developerId> from the developer-account axis;
when the Console-issued URI differs, pass it with --bucket (copy it with the
"Copy Cloud Storage URI" button on the Console's Download reports > ` + f.Page + `
page).

--month YYYY-MM reads one month; when omitted, the latest month present for
the package. --since YYYY-MM --until YYYY-MM read every month of the range
and merge them into one result set: the columns are the union of the
months' columns, and a month with no report is skipped with a warning.

Each month comes as one file per dimension. --dimension picks it and is
checked against the bucket, so a dimension Google adds works the day it
appears. ` + defaultSplitText(f) + `
Run gplay reports list to see the months and dimensions present.

--output json emits {"report": {...}, "columns": [{"key", "header"}, ...],
"rows": [{...}]}. Keys derive from the CSV header (words lowerCamel-joined:
"Daily Device Installs" is dailyDeviceInstalls), and every value is the
CSV's string, verbatim (a deliberate exception to the verbatim API rule: the
upstream is a CSV file). A column Google adds appears with no gplay release.
The table shows every column except these, which --columns can still pick:
` + strings.Join(f.TableHidden, ", ") + `.
--output markdown renders a Markdown table.`
}

func defaultSplitText(f reports.Family) string {
	if f.DefaultSplit == "" {
		return "Without it, the error names the dimensions present."
	}
	return "Without it, the " + f.DefaultSplit + " file is read when present;\notherwise the error names the dimensions present."
}

func example(f reports.Family) string {
	return `  # The latest month present (the ` + f.DefaultSplit + ` file)
  gplay reports ` + f.Name + `

  gplay reports ` + f.Name + ` --month 2026-09 --dimension country

  # Half a year merged into one result set, as JSON
  gplay reports ` + f.Name + ` --since 2026-01 --until 2026-06 --dimension country --output json`
}
