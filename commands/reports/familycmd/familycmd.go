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
	"strconv"
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
	Product     string // --product, PerProduct families only (subscription stats)
	Month       string // --month YYYY-MM; "" with no range = the latest month present
	Since       string // --since YYYY-MM, range start (with --until)
	Until       string // --until YYYY-MM, range end (with --since)
	Dimension   string // --dimension; "" = the family's default split when present
	Bucket      string // --bucket override; "" = derive from the developer-id
	DeveloperID string // --developer-id (feeds the default bucket name only)
	Columns     string // --columns; "" = the family's default table columns
	// IncludeBuyerLocation is --include-buyer-location: keep the buyer's
	// city, state and postcode a BuyerLocation family hides by default.
	IncludeBuyerLocation bool
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
	pkg, err := packageOf(rc, f, in.Package)
	if err != nil {
		return nil, err
	}
	months, err := requestedMonths(in)
	if err != nil {
		return nil, err
	}
	product := strings.TrimSpace(in.Product)
	if f.PerProduct && product == "" {
		return nil, exit.Usagef("%s needs --product <id>, the subscription product to read: "+
			"gplay reports list --family %s --package %s shows the product ids present", f.Name, f.Name, pkg)
	}
	bucket, err := reportbucket.Resolve(rc, in.Bucket, in.DeveloperID)
	if err != nil {
		return nil, err
	}
	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	prefix := f.ListPrefix(pkg)
	if f.PerProduct {
		prefix = f.ProductListPrefix(pkg, product)
	}
	listing, err := gcs.ListObjects(rc.Ctx, hc, bucket, prefix)
	if err != nil {
		return nil, reportbucket.Classify(bucket, f.Permission, f.Page, err)
	}
	objs, skipped := f.Objects(listing, pkg)
	for _, name := range skipped {
		rc.Logf("ignoring %s: not a %s export name", name, f.Name)
	}
	if f.PerProduct {
		// The product prefix also matches a longer id (gold_ and gold_trial):
		// the parsed product decides, as the parsed package does.
		objs = onlyProduct(objs, product)
	}
	sel, err := f.Select(objs, months, strings.TrimSpace(in.Dimension))
	var splitErr *reports.SplitError
	switch {
	case errors.Is(err, reports.ErrNoReport):
		return nil, noReport(f, pkg, product, bucket, months)
	case errors.As(err, &splitErr):
		return nil, exit.Usagef("%s", err)
	case err != nil:
		return nil, err
	}
	for _, m := range sel.Missing {
		if f.Scope == reports.AccountWide {
			rc.Warnf("no %s report for %s: skipped", f.Name, reports.DisplayMonth(m))
			continue
		}
		rc.Warnf("no %s report for %s with dimension %q: skipped", f.Name, reports.DisplayMonth(m), sel.Split)
	}
	// The listing gives each export's size: one over the read cap is refused
	// before a byte of it is fetched (ADR-0050 §10).
	for _, o := range sel.Objects {
		if size, err := strconv.ParseInt(o.Size, 10, 64); err == nil && size > f.MaxObjectBytes {
			return nil, tooLarge(o.Name, f.MaxObjectBytes)
		}
	}

	tables := make([]reports.Table, 0, len(sel.Objects))
	id := reports.Identity{Family: f.Name, Package: pkg, Product: product, Split: sel.Split}
	for _, m := range sel.Months {
		id.Months = append(id.Months, reports.DisplayMonth(m))
	}
	for _, o := range sel.Objects {
		raw, err := gcs.FetchObjectLimit(rc.Ctx, hc, bucket, o.Name, f.MaxObjectBytes)
		if errors.Is(err, gcs.ErrTooLarge) {
			// The object grew past the cap since it was listed.
			return nil, tooLarge(o.Name, f.MaxObjectBytes)
		}
		if err != nil {
			return nil, reportbucket.Classify(bucket, f.Permission, f.Page, err)
		}
		t, err := f.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", o.Name, err)
		}
		// Each export is filtered on the package column its own header
		// declares, before the merge: a range across a rename of that column
		// (Product ID one month, Package Name the next) keeps every month.
		if f.Scope == reports.AccountWide && pkg != "" {
			if t, err = reports.FilterPackage(t, pkg); err != nil {
				return nil, fmt.Errorf("%s: %w", o.Name, err)
			}
		}
		tables = append(tables, t)
		id.Objects = append(id.Objects, o.Name)
	}
	merged := reports.Merge(tables...)
	if f.BuyerLocation && !in.IncludeBuyerLocation {
		var hidden []reports.Column
		merged, hidden = reports.MaskBuyerLocation(merged)
		if len(hidden) > 0 {
			headers := make([]string, len(hidden))
			for i, c := range hidden {
				headers[i] = c.Header
			}
			rc.Notef("%d buyer-location columns hidden (%s): pass --include-buyer-location to show them", len(hidden), strings.Join(headers, ", "))
		}
	}
	report := reports.Report{Identity: id, Table: merged}

	cols, err := tableColumns(f, report.Table.Columns, in.Columns)
	if err != nil {
		return nil, err
	}
	return Payload{Report: report, Columns: cols}, nil
}

// packageOf resolves the package a family reads. A per-package family needs
// one (--package, else the Project pin). An account-wide family covers every
// app: --package only filters its rows, so the pin, which says which app a
// repository ships rather than which rows to read, does not apply.
func packageOf(rc *kernel.RunContext, f reports.Family, flag string) (string, error) {
	if f.Scope == reports.AccountWide {
		return strings.TrimSpace(flag), nil
	}
	return rc.Package(flag)
}

// tooLarge is the refusal of an export over the family's read cap: exit 20,
// like every other bound, never a truncated read.
func tooLarge(object string, limit int64) error {
	return &reports.RefusedError{Object: object, Reason: fmt.Sprintf("larger than the %d-byte read cap", limit)}
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
func noReport(f reports.Family, pkg, product, bucket string, months []string) error {
	when := ""
	switch len(months) {
	case 0:
	case 1:
		when = " for " + reports.DisplayMonth(months[0])
	default:
		when = " for " + reports.DisplayMonth(months[0]) + ".." + reports.DisplayMonth(months[len(months)-1])
	}
	if f.Scope == reports.AccountWide {
		return &noReportError{msg: fmt.Sprintf("no %s report%s in bucket %s: Play publishes a month after it ends; "+
			"pick a published month (gplay reports list --family %s), or pass --bucket if the derived name is wrong",
			f.Name, when, bucket, f.Name)}
	}
	what, check := pkg, "--package"
	if product != "" {
		what, check = pkg+" product "+product, "--package and --product"
	}
	return &noReportError{msg: fmt.Sprintf("no %s report for %s%s in bucket %s: Play publishes a month a few days after it ends; "+
		"check %s, pick a published month (gplay reports list --family %s --package %s), "+
		"or pass --bucket if the derived name is wrong", f.Name, what, when, bucket, check, f.Name, pkg)}
}

// onlyProduct keeps the exports of one product.
func onlyProduct(objs []reports.Object, product string) []reports.Object {
	kept := objs[:0:0]
	for _, o := range objs {
		if o.Product == product {
			kept = append(kept, o)
		}
	}
	return kept
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
	if f.Scope == reports.AccountWide {
		cmd.Flags().StringVar(&in.Package, "package", "", "keep only this Android package's rows, matched on the report's package column (default: every app; the .gplay/config.json pin does not apply)")
	} else {
		cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	}
	if f.PerProduct {
		cmd.Flags().StringVar(&in.Product, "product", "", "subscription product id to read (required; gplay reports list shows the ids present)")
	}
	cmd.Flags().StringVar(&in.Month, "month", "", "month to read as YYYY-MM (default: the latest month present in the bucket)")
	cmd.Flags().StringVar(&in.Since, "since", "", "range start as YYYY-MM (with --until; mutually exclusive with --month)")
	cmd.Flags().StringVar(&in.Until, "until", "", "range end as YYYY-MM (with --since; mutually exclusive with --month)")
	if f.Scope == reports.PerPackage {
		cmd.Flags().StringVar(&in.Dimension, "dimension", "", dimensionHelp(f))
	}
	if f.BuyerLocation {
		cmd.Flags().BoolVar(&in.IncludeBuyerLocation, "include-buyer-location", false, "show the buyer's city, state and postcode (hidden by default; the buyer's country is always shown)")
	}
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

--month YYYY-MM reads one month; when omitted, the latest month present` + forThePackage(f) + `.
--since YYYY-MM --until YYYY-MM read every month of the range and merge them
into one result set: the columns are the union of the months' columns, and a
month with no report is skipped with a warning.

` + selectionText(f) + `--output json emits {"report": {...}, "columns": [{"key", "header"}, ...],
"rows": [{...}]}. Keys derive from the CSV header (words lowerCamel-joined:
"Daily Device Installs" is dailyDeviceInstalls), and every value is the
CSV's string, verbatim (a deliberate exception to the verbatim API rule: the
upstream is a CSV file). A column Google adds appears with no gplay release.
The table shows every column except these, which --columns can still pick:
` + strings.Join(f.TableHidden, ", ") + `.
--output markdown renders a Markdown table.`
}

func forThePackage(f reports.Family) string {
	if f.Scope == reports.AccountWide {
		return ""
	}
	return " for\nthe package"
}

// selectionText is the --help part that differs by family shape: the
// dimension of a per-package family; the row filter, the buyer-location
// default and the ZIP bounds of an account-wide finance family.
func selectionText(f reports.Family) string {
	if f.Scope == reports.PerPackage {
		return `Each month comes as one file per dimension. --dimension picks it and is
checked against the bucket, so a dimension Google adds works the day it
appears. ` + defaultSplitText(f) + `
Run gplay reports list to see the months and dimensions present.

`
	}
	text := `The report covers every app of the developer account. --package keeps the
rows of one app, matched on the package column of the CSV header (Package
Name, Package ID, or Product ID as the finance reports call it); the Project
pin does not apply. Run gplay reports list --family ` + f.Name + ` to see the months
present.

`
	if f.BuyerLocation {
		text += `The buyer's city, state and postcode are hidden by default, from the table
and the JSON alike, and a stderr notice says how many columns were hidden;
--include-buyer-location shows them. The buyer's country always stays.

`
	}
	if f.Container == reports.ZIP {
		text += `The export is a ZIP read under fixed bounds: at most 128 MiB compressed,
16 members, 1 GiB decompressed per member and in total. Only its .csv
members are read, each decoded by its byte-order mark. An export over a
bound fails with exit 20, never with a truncated report.

`
	}
	return text
}

func defaultSplitText(f reports.Family) string {
	if f.DefaultSplit == "" {
		return "Without it, the error names the dimensions present."
	}
	return "Without it, the " + f.DefaultSplit + " file is read when present;\notherwise the error names the dimensions present."
}

func example(f reports.Family) string {
	if f.Scope == reports.AccountWide {
		return accountWideExample(f)
	}
	sel := ""
	if f.PerProduct {
		sel = " --product premium_monthly"
	}
	first := `  # The latest month present (the ` + f.DefaultSplit + ` file)
  gplay reports ` + f.Name + sel
	if f.DefaultSplit == "" {
		first = `  # The latest month present, by country
  gplay reports ` + f.Name + sel + ` --dimension country`
	}
	return first + `

  gplay reports ` + f.Name + sel + ` --month 2026-09 --dimension country

  # Half a year merged into one result set, as JSON
  gplay reports ` + f.Name + sel + ` --since 2026-01 --until 2026-06 --dimension country --output json`
}

func accountWideExample(f reports.Family) string {
	one := `  # One app's rows of one month
  gplay reports ` + f.Name + ` --month 2026-09 --package com.example.app`
	if f.BuyerLocation {
		one += `

  # The same, with the buyer's city, state and postcode
  gplay reports ` + f.Name + ` --month 2026-09 --package com.example.app --include-buyer-location`
	}
	return `  # The latest month present, every app
  gplay reports ` + f.Name + `

` + one + `

  # Half a year merged into one result set, as JSON
  gplay reports ` + f.Name + ` --since 2026-01 --until 2026-06 --output json`
}
