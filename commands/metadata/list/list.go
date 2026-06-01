// Package list implements `gplay metadata list`: a read-only summary of
// the Store front Listings live on Google Play for a package — one row per
// locale, showing which managed fields each locale has and the character
// length of each. It reads ONLY from Play (never the local metadata tree):
// resolve --package, open a read-only Edit, consume
// internal/play/listings.List, project each API Listing onto the four
// managed fields, and render. The Edit is opened and discarded via
// edits.WithReadOnlyEdit, never committed.
//
// Comparing the live Listings against an on-disk tree is the job of
// `gplay metadata apply --dry-run`; this command answers the simpler "what
// does Play currently hold, per locale?".
package list

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
}

// usageError is a CLI-misuse error (no package); ExitCode()=2 per
// docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func (e *usageError) ExitCode() int { return 2 }

// packageNotFoundError wraps an edits.insert 404 with a hint pointing at
// `gplay apps list`. Like in `tracks list`, it carries no ExitCode of its
// own so the wrapped *api.Error (404 -> exit 30) stays authoritative
// through the Coder chain — an unknown package is an API 4xx, not a CLI
// misuse.
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found — run `gplay apps list` to see the packages registered with gplay: %v", e.pkg, e.cause)
}

func (e *packageNotFoundError) Unwrap() error { return e.cause }

// forbiddenError wraps a 403 (service account not invited on the app) with
// the standard grant-access hint. It carries no ExitCode of its own so the
// wrapped *api.Error (403 -> exit 11) stays authoritative.
type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q — in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.pkg, e.cause)
}

func (e *forbiddenError) Unwrap() error { return e.cause }

// classifyEditError adds an actionable hint to the two operator-facing
// failures of a read-only listings read — an unknown package (404) and a
// service account that has not been invited on the app (403) — while
// leaving the wrapped *api.Error to drive the exit code. Every other
// failure (5xx, network, edit conflict) propagates verbatim.
func classifyEditError(pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return &packageNotFoundError{pkg: pkg, cause: err}
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		}
	}
	return err
}

// ListingRow is the synthesized, one-line-per-locale view rendered by the
// table and markdown formatters. It is NOT the API shape: Sizes holds the
// character length of each managed field present (non-empty) on the
// locale, keyed by listing.Field; an absent (or empty) API field has no
// entry and renders as a blank cell. The JSON view bypasses ListingRow
// entirely and emits the raw edits.listings.list payload (ADR-0003).
type ListingRow struct {
	Locale string
	Sizes  map[listing.Field]int
}

// fieldValue returns the value of the managed field f on an API Listing.
// The four managed fields map onto the API struct's keys 1:1 (title,
// shortDescription, fullDescription, video); an out-of-range Field yields
// "".
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

// BuildRows projects each API Listing onto the four managed fields,
// recording the character length of every present (non-empty) field. An
// empty API field ("") is treated as absent per the ADR-0011 "missing ≠
// empty" display rule for this read-only summary — there is no size to
// report for a field Play holds empty, so its cell stays blank. Rows
// follow the API's locale order (List returns them as Play does).
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

// columnDef pairs a column's header with the managed Field it reports the
// size of. The header column for the locale itself is handled separately.
type columnDef struct {
	header string
	field  listing.Field
}

// fieldColumns is the ordered list of per-field size columns, derived from
// listing.Specs() so the order (title, short, full, video) and the set of
// fields stay in lockstep with the shared model. Headers are gplay-chosen
// display names matching the table vocabulary.
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

// Payload satisfies output.Renderable. Raw carries the edits.listings.list
// body for the ADR-0003 JSON pass-through; Rows is the synthesized
// one-line-per-locale size summary.
type Payload struct {
	Raw  json.RawMessage `json:"-"`
	Rows []ListingRow    `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format. The
// JSON form is the ADR-0003 edits.listings.list pass-through; table and
// markdown are human-shaped, one-row-per-locale field-size views.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// headers returns the table header row: LOCALE plus one column per managed
// field.
func headers() []string {
	h := make([]string, 0, len(fieldColumns)+1)
	h = append(h, "LOCALE")
	for _, c := range fieldColumns {
		h = append(h, c.header)
	}
	return h
}

// row extracts one locale's cells: the locale code, then the character
// count of each managed field present on it (blank when the field is
// absent/empty).
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

// renderTable writes a tab-aligned table: LOCALE + one field-size column
// per managed field, one row per locale.
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

// renderJSON emits the raw edits.listings.list body verbatim (ADR-0003
// pass-through). Raw is always populated on the Run path; an empty Raw
// would mean we never captured the API body, so we error rather than
// synthesize one (every Payload field is json:"-", so falling back to
// output.WriteJSON would emit a bare "{}" and silently break the
// contract). Mirrors `tracks list`'s renderJSON.
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw listings.list payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderMarkdown writes the locale × field-size grid as a GitHub-Flavored
// Markdown table via output.MarkdownTable.
func renderMarkdown(w io.Writer, p Payload) error {
	rows := make([][]string, 0, len(p.Rows))
	for _, r := range p.Rows {
		rows = append(rows, row(r))
	}
	return output.MarkdownTable(w, headers(), rows)
}

// Run is the business function the kernel invokes. It resolves the
// package, builds an authenticated HTTP client, then opens a read-only
// Edit and lists every locale's Listing.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
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
		return nil, classifyEditError(pkg, err)
	}

	return Payload{Raw: raw, Rows: BuildRows(parsed)}, nil
}

// NewCommand returns the cobra command for `gplay metadata list`.
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
