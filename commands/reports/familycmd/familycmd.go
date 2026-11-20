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

var limits = reports.DefaultLimits

type Input struct {
	Package              string
	Product              string
	Month                string
	Since                string
	Until                string
	Dimension            string
	Bucket               string
	DeveloperID          string
	Columns              string
	IncludeBuyerLocation bool
}

type Payload struct {
	Report  reports.Report
	Columns []output.Column[reports.Row]
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Columns, p.Report.Table.Rows) },
		JSON:     p.Report.WriteJSON,
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Columns, p.Report.Table.Rows) },
	}
}

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
	hc, err := rc.UploadClient()
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
	for _, o := range sel.Objects {
		if size, err := strconv.ParseInt(o.Size, 10, 64); err == nil && size > f.MaxObjectBytes {
			return nil, tooLarge(o.Name, f.MaxObjectBytes)
		}
	}

	tables := make([]reports.Table, 0, len(sel.Objects))
	id := reports.Identity{Family: f.Name, Package: pkg, Product: product, Split: sel.Split}
	filter := ""
	if f.Scope == reports.AccountWide {
		filter = pkg
	}
	rd := reports.NewReaderWithLimits(filter, limits)
	for _, o := range sel.Objects {
		raw, err := gcs.FetchObjectLimit(rc.Ctx, hc, bucket, o.Name, f.MaxObjectBytes)
		if errors.Is(err, gcs.ErrTooLarge) {
			return nil, tooLarge(o.Name, f.MaxObjectBytes)
		}
		if reportbucket.IsNotFound(err) {
			if in.Since != "" {
				rc.Warnf("no %s report for %s: %s vanished between the listing and the read: skipped", f.Name, reports.DisplayMonth(o.Month), o.Name)
				continue
			}
			return nil, &vanishedError{object: o.Name, bucket: bucket, cause: err}
		}
		if err != nil {
			return nil, reportbucket.Classify(bucket, f.Permission, f.Page, err)
		}
		t, err := rd.Read(o.Name, raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", o.Name, err)
		}
		tables = append(tables, t)
		id.Objects = append(id.Objects, o.Name)
		if m := reports.DisplayMonth(o.Month); len(id.Months) == 0 || id.Months[len(id.Months)-1] != m {
			id.Months = append(id.Months, m)
		}
	}
	if len(id.Objects) == 0 {
		return nil, noReport(f, pkg, product, bucket, months)
	}
	if filter != "" && rd.RowsRead() > 0 && rd.RowsKept() == 0 {
		rc.Notef("0 of %d rows matched --package %s", rd.RowsRead(), pkg)
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

func packageOf(rc *kernel.RunContext, f reports.Family, flag string) (string, error) {
	if f.Scope == reports.AccountWide {
		return strings.TrimSpace(flag), nil
	}
	return rc.Package(flag)
}

func tooLarge(object string, limit int64) error {
	return &reports.RefusedError{Object: object, Reason: fmt.Sprintf("larger than the %d-byte read cap", limit)}
}

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

func onlyProduct(objs []reports.Object, product string) []reports.Object {
	kept := objs[:0:0]
	for _, o := range objs {
		if o.Product == product {
			kept = append(kept, o)
		}
	}
	return kept
}

type vanishedError struct {
	object, bucket string
	cause          error
}

func (e *vanishedError) Error() string {
	return e.object + " vanished between the listing and the read of bucket " + e.bucket +
		" (Play republishes an export now and then): run the command again: " + e.cause.Error()
}

func (e *vanishedError) Unwrap() error { return e.cause }

type noReportError struct{ msg string }

func (e *noReportError) Error() string { return e.msg }
func (e *noReportError) ExitCode() int { return 30 }

func tableColumns(f reports.Family, cols []reports.Column, spec string) ([]output.Column[reports.Row], error) {
	all := make([]output.Column[reports.Row], len(cols))
	for i, c := range cols {
		key := c.Key
		header := c.Header
		if header == "" {
			header = key
		}
		all[i] = output.Column[reports.Row]{Key: key, Header: header, Value: func(r reports.Row) string { return r.Value(key) }}
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
One command reads at most 512 MiB of CSV and holds at most 24 Mi cells (a
row counts as its cells plus two), every month of a range together: past a
bound it fails with exit 20 naming it, never with a truncated report, and a
narrower --since/--until reads.
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
		text += `The export is a ZIP read under fixed bounds: at most 128 MiB compressed and
16 members. Only its .csv members are read, each decoded by its byte-order
mark; an export named .csv is read as the CSV it is.

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
