package list

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

type Input struct {
	Package string
}

type ListingRow struct {
	Locale string
	Sizes  map[listing.Field]int
}

func fieldValue(l listings.Listing, f listing.Field) string {
	switch f {
	case listing.Title:
		return l.Title
	case listing.ShortDescription:
		return l.ShortDescription
	case listing.FullDescription:
		return l.FullDescription
	case listing.Video:
		return l.Video
	default:
		return ""
	}
}

func BuildRows(apiListings []listings.Listing) []ListingRow {
	rows := make([]ListingRow, 0, len(apiListings))
	for _, l := range apiListings {
		row := ListingRow{Locale: l.Language, Sizes: make(map[listing.Field]int)}
		for _, f := range listing.Fields() {
			v := fieldValue(l, f)
			if v == "" {
				continue
			}
			row.Sizes[f] = listing.CharCount(v)
		}
		rows = append(rows, row)
	}
	return rows
}

type columnDef struct {
	header string
	field  listing.Field
}

var fieldColumns = buildFieldColumns()

func buildFieldColumns() []columnDef {
	headers := map[listing.Field]string{
		listing.Title:            "TITLE",
		listing.ShortDescription: "SHORT_DESC",
		listing.FullDescription:  "FULL_DESC",
		listing.Video:            "VIDEO",
	}
	cols := make([]columnDef, 0, len(listing.Specs()))
	for _, s := range listing.Specs() {
		cols = append(cols, columnDef{header: headers[s.Field], field: s.Field})
	}
	return cols
}

type Payload struct {
	Raw  json.RawMessage `json:"-"`
	Rows []ListingRow    `json:"-"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func headers() []string {
	h := make([]string, 0, len(fieldColumns)+1)
	h = append(h, "LOCALE")
	for _, c := range fieldColumns {
		h = append(h, c.header)
	}
	return h
}

func row(r ListingRow) []string {
	cells := make([]string, 0, len(fieldColumns)+1)
	cells = append(cells, r.Locale)
	for _, c := range fieldColumns {
		if n, ok := r.Sizes[c.field]; ok {
			cells = append(cells, strconv.Itoa(n))
		} else {
			cells = append(cells, "")
		}
	}
	return cells
}

func renderTable(w io.Writer, p Payload) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(headers(), "\t")); err != nil {
		return err
	}
	for _, r := range p.Rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row(r), "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func renderJSON(w io.Writer, p Payload) error {
	_, err := w.Write(p.Raw)
	return err
}

func renderMarkdown(w io.Writer, p Payload) error {
	rows := make([][]string, 0, len(p.Rows))
	for _, r := range p.Rows {
		rows = append(rows, row(r))
	}
	return output.MarkdownTable(w, headers(), rows)
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	var (
		parsed []listings.Listing
		raw    json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		ls, r, e := listings.List(rc.Ctx, httpClient, pkg, editID)
		if e != nil {
			return e
		}
		parsed, raw = ls, r
		return nil
	}); err != nil {
		return nil, apihint.ForPackage(pkg, err)
	}

	return Payload{Raw: raw, Rows: BuildRows(parsed)}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Summarize the Store front Listings live on Play, per locale",
		Long: `Summarize the Store front Listings currently live on Google Play for
--package: one row per locale, showing which managed fields the locale has
and the character length of each (TITLE, SHORT_DESC, FULL_DESC, VIDEO). A
field Play holds empty leaves a blank cell.

Reads the Listings inside a read-only Edit (open → listings.list →
discard); nothing is committed and nothing on disk is read. Comparing the
live Listings against an on-disk metadata tree is the job of ` +
			"`gplay metadata apply --dry-run`" + `.

(--output json is the raw edits.listings.list payload; --output markdown
renders a Markdown table.)`,
		Example: `  gplay metadata list
  gplay metadata list --package com.example.app --output markdown
  gplay metadata list --output json`,
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
	return cmd
}
