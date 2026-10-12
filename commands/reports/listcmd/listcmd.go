// Package listcmd implements `gplay reports list`: the inventory of the Bulk
// reports the Reporting bucket holds, one row per export (family, package,
// month, dimension), so a user sees what exists before asking for one
// (ADR-0050). One storage.objects.list per family of the catalogue; a family
// the service account may not read is a warning, not a failure, so the rest
// of the inventory still prints.
package listcmd

import (
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

// catalogue is the family set listed; tests swap in a finance family before
// one ships, to exercise the per-family 403 path.
var catalogue = reports.Catalogue

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Family      string // --family; "" = every family
	Package     string // --package; "" = every package (no Project pin: an inventory)
	Since       string // --since YYYY-MM, first month kept
	Until       string // --until YYYY-MM, last month kept
	Bucket      string
	DeveloperID string
	Columns     string
}

// Entry is one export in the inventory: a row of `reports list`.
type Entry struct {
	Family  string `json:"family"`
	Package string `json:"package,omitempty"`
	Product string `json:"product,omitempty"` // subscription stats: the id --product takes
	Month   string `json:"month"`             // YYYY-MM
	Split   string `json:"split,omitempty"`
	Object  string `json:"object"`
	Size    string `json:"size"`    // bytes, as the bucket listing reports them
	Updated string `json:"updated"` // RFC 3339, as the bucket listing reports it
}

var columns = output.NewColumnSet(
	output.Column[Entry]{Key: "family", Header: "FAMILY", Value: func(e Entry) string { return e.Family }},
	output.Column[Entry]{Key: "package", Header: "PACKAGE", Value: func(e Entry) string { return e.Package }},
	output.Column[Entry]{Key: "product", Header: "PRODUCT", Value: func(e Entry) string { return e.Product }},
	output.Column[Entry]{Key: "month", Header: "MONTH", Value: func(e Entry) string { return e.Month }},
	output.Column[Entry]{Key: "split", Header: "DIMENSION", Value: func(e Entry) string { return e.Split }},
	output.Column[Entry]{Key: "size", Header: "SIZE", Value: func(e Entry) string { return e.Size }},
	output.Column[Entry]{Key: "updated", Header: "UPDATED", Value: func(e Entry) string { return e.Updated }},
	output.Column[Entry]{Key: "object", Header: "OBJECT", Value: func(e Entry) string { return e.Object }},
)

const defaultColumns = "family,package,product,month,split,size,updated"

// Payload satisfies output.Renderable.
type Payload struct {
	Entries []Entry
	Columns []output.Column[Entry]
}

// Renderers draws the inventory; JSON is {"reports": [...]}, every field.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table: func(w io.Writer) error { return output.RenderTable(w, p.Columns, p.Entries) },
		JSON: func(w io.Writer) error {
			return output.WriteJSON(w, struct {
				Reports []Entry `json:"reports"`
			}{output.NonNil(p.Entries)})
		},
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Columns, p.Entries) },
	}
}

// Run validates the filters, then lists each family's exports.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	families := catalogue()
	if name := strings.TrimSpace(in.Family); name != "" {
		var found []reports.Family
		names := make([]string, len(families))
		for i, f := range families {
			names[i] = f.Name
			if f.Name == name {
				found = []reports.Family{f}
			}
		}
		if found == nil {
			return nil, exit.Usagef("unknown --family %q (known: %s)", name, strings.Join(names, ", "))
		}
		families = found
	}
	since, until, err := window(in.Since, in.Until)
	if err != nil {
		return nil, err
	}
	spec := in.Columns
	if strings.TrimSpace(spec) == "" {
		spec = defaultColumns
	}
	cols, err := columns.Resolve(spec)
	if err != nil {
		return nil, err
	}
	pkg := strings.TrimSpace(in.Package)
	bucket, err := reportbucket.Resolve(rc, in.Bucket, in.DeveloperID)
	if err != nil {
		return nil, err
	}
	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	type denial struct {
		f   reports.Family
		err error
	}
	var (
		entries []Entry
		denied  []denial
	)
	for _, f := range families {
		listing, err := gcs.ListObjects(rc.Ctx, hc, bucket, f.ListPrefix(pkg))
		if err != nil {
			if reportbucket.IsForbidden(err) {
				denied = append(denied, denial{f, err})
				continue
			}
			return nil, reportbucket.Classify(bucket, f.Permission, f.Page, err)
		}
		objs, skipped := f.Objects(listing, pkg)
		for _, name := range skipped {
			rc.Logf("ignoring %s: not a %s export name", name, f.Name)
		}
		for _, o := range objs {
			if (since != "" && o.Month < since) || (until != "" && o.Month > until) {
				continue
			}
			entries = append(entries, Entry{
				Family: o.Family, Package: o.Package, Product: o.Product, Month: reports.DisplayMonth(o.Month), Split: o.Split,
				Object: o.Name, Size: o.Size, Updated: o.Updated,
			})
		}
	}
	// Nothing could be read at all: that is the access failure itself, with
	// the permission and Console hint, not an empty inventory.
	if len(denied) == len(families) {
		d := denied[0]
		return nil, reportbucket.Classify(bucket, d.f.Permission, d.f.Page, d.err)
	}
	for _, d := range denied {
		rc.Warnf("%s reports skipped: the service account lacks the %q (global) permission in the Play Console (403)", d.f.Name, string(d.f.Permission))
	}
	return Payload{Entries: entries, Columns: cols}, nil
}

// window validates the optional --since/--until month bounds (each may come
// alone: they filter an inventory, not a fetched range).
func window(since, until string) (string, string, error) {
	var s, u string
	var err error
	if since != "" {
		if s, err = reports.NormalizeMonth(since); err != nil {
			return "", "", exit.Usagef("invalid --since %q: want YYYY-MM (e.g. 2026-06)", since)
		}
	}
	if until != "" {
		if u, err = reports.NormalizeMonth(until); err != nil {
			return "", "", exit.Usagef("invalid --until %q: want YYYY-MM (e.g. 2026-06)", until)
		}
	}
	if s != "" && u != "" && s > u {
		return "", "", exit.Usagef("--since %s is after --until %s", since, until)
	}
	return s, u, nil
}

// NewCommand returns the cobra command for `gplay reports list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the Bulk reports the Reporting bucket holds (family, package, month, dimension)",
		Long: `List the Bulk reports the Reporting bucket holds: one row per export, with
its family, package, product (subscription stats), month, dimension, size
and last update. It is the way to see which months, dimensions and product
ids exist before reading one with gplay reports <family>.

Known families: ` + strings.Join(reports.Names(), ", ") + `.
Each is listed with one Cloud Storage call (read-only scope,
devstorage.read_only). A family the service account may not read is skipped
with a warning naming the Play Console permission it needs, so the rest of
the inventory still prints; when no family can be read, the command fails
with that permission and the "Copy Cloud Storage URI" hint for --bucket.

--family, --package, --since YYYY-MM and --until YYYY-MM narrow the
inventory (each bound may come alone). Without --package every package is
listed: the Project pin does not apply to an inventory.

--output json emits {"reports": [{"family", "package", "product", "month",
"split", "object", "size", "updated"}]}; product is present on subscription
stats rows only. size and updated are the bucket listing's strings, verbatim.
Without --package, a subscription stats name is split at the first
underscore after the prefix (package, then product id); pass --package when
the package itself holds an underscore.`,
		Example: `  # Everything the bucket holds
  gplay reports list

  # Which months and dimensions of installs exist for one app this year
  gplay reports list --family installs --package com.example.app --since 2026-01`,
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
	cmd.Flags().StringVar(&in.Family, "family", "", "only this report family ("+strings.Join(reports.Names(), ", ")+")")
	cmd.Flags().StringVar(&in.Package, "package", "", "only this Android package (default: every package)")
	cmd.Flags().StringVar(&in.Since, "since", "", "first month to list, as YYYY-MM")
	cmd.Flags().StringVar(&in.Until, "until", "", "last month to list, as YYYY-MM")
	cmd.Flags().StringVar(&in.Bucket, "bucket", "", "Reporting bucket name override (default: pubsite_prod_rev_<developerId>)")
	cmd.Flags().StringVar(&in.DeveloperID, "developer-id", "", "Play Console Developer account id used to derive the bucket (overrides the active Account's, env, and project-local)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+defaultColumns+"; also: object)")
	return cmd
}
