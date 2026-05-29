// Package list implements `gplay tracks list`: a read-only, cross-track
// listing of every track configured for a package — the four standard
// tracks (internal, alpha, beta, production) plus any custom closed
// tracks the service account can see — with a one-line summary of the
// top release on each. It is thin glue — resolve --package, open a
// read-only Edit, consume internal/play/tracks.List, synthesize one row
// per track, and render. The Edit is opened and discarded via
// edits.WithReadOnlyEdit, never committed.
//
// Single-track deep listing (every coexisting release on one track) is
// the job of `gplay releases list --track <T>`.
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
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Columns string
}

// usageError is a CLI-misuse error (no package, unknown column);
// ExitCode()=2 per docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func (e *usageError) ExitCode() int { return 2 }

// packageNotFoundError wraps an edits.insert 404 with a hint pointing at
// `gplay apps list`. Like trackNotFoundError in `releases list`, it
// carries no ExitCode of its own so the wrapped *api.Error (404 -> exit
// 30) stays authoritative through the Coder chain — an unknown package is
// an API 4xx, not a CLI misuse.
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found — run `gplay apps list` to see the packages registered with gplay: %v", e.pkg, e.cause)
}

func (e *packageNotFoundError) Unwrap() error { return e.cause }

// forbiddenError wraps a 403 (service account not invited on the app)
// with the standard grant-access hint. It carries no ExitCode of its own
// so the wrapped *api.Error (403 -> exit 11) stays authoritative.
type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q — in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.pkg, e.cause)
}

func (e *forbiddenError) Unwrap() error { return e.cause }

// classifyEditError adds an actionable hint to the two operator-facing
// failures of a read-only tracks listing — an unknown package (404) and a
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

// StandardTracks are the four well-known tracks Google Play provisions
// for every app, in promotion order. They always appear in the
// table/markdown view — even when edits.tracks.list omits one because it
// was never configured — so the operator sees the track exists. The
// order here is the row order applied before any custom tracks.
var StandardTracks = []string{"internal", "alpha", "beta", "production"}

// TrackRow is the synthesized, one-line-per-track view rendered by the
// table and markdown formatters. It is NOT the API shape: Kind is
// derived (standard vs custom) and the release fields summarize the
// track's top release (see BuildRows). The JSON view bypasses TrackRow
// entirely and emits the raw API payload (ADR-0003).
type TrackRow struct {
	Track        string
	Kind         string // "standard" | "custom"
	Release      string // top release name; "" when the track has no release
	Status       string // top release status (completed/inProgress/halted/draft)
	UserFraction float64
	VersionCodes []string
	HasRelease   bool
}

// BuildRows turns the raw edits.tracks.list result into one row per
// track for the table/markdown views. The four standard tracks always
// appear first, in canonical order, marked kind=standard — even when the
// API omitted a never-configured one (its row is empty). Custom closed
// tracks the API returned follow, in API order, marked kind=custom.
func BuildRows(apiTracks []tracks.Track) []TrackRow {
	byName := make(map[string]tracks.Track, len(apiTracks))
	standard := make(map[string]bool, len(StandardTracks))
	for _, t := range apiTracks {
		byName[t.Track] = t
	}
	for _, s := range StandardTracks {
		standard[s] = true
	}

	rows := make([]TrackRow, 0, len(StandardTracks)+len(apiTracks))
	for _, s := range StandardTracks {
		rows = append(rows, makeRow(s, "standard", byName[s]))
	}
	for _, t := range apiTracks {
		if standard[t.Track] {
			continue
		}
		rows = append(rows, makeRow(t.Track, "custom", t))
	}
	return rows
}

// makeRow builds a single TrackRow, summarizing the track's top release.
// t is the zero Track when a standard track was never configured (no API
// entry), which yields a row with HasRelease=false and empty release
// fields.
func makeRow(name, kind string, t tracks.Track) TrackRow {
	row := TrackRow{Track: name, Kind: kind}
	if top, ok := topRelease(t.Releases); ok {
		row.HasRelease = true
		row.Release = top.Name
		row.Status = top.Status
		row.UserFraction = top.UserFraction
		row.VersionCodes = top.VersionCodes
	}
	return row
}

// topRelease picks the release with the highest version code — the newest
// build on the track — as the one-line summary. A track can carry several
// coexisting releases (e.g. an inProgress staged rollout above a completed
// one, or a draft staged above production); the highest version code is
// the deterministic, order-independent "what is newest here" signal, and
// the status column tells the reader whether that newest build is a draft.
// Returns ok=false for a track with no releases.
func topRelease(rels []tracks.Release) (tracks.Release, bool) {
	if len(rels) == 0 {
		return tracks.Release{}, false
	}
	best := rels[0]
	bestCode := maxVersionCode(best.VersionCodes)
	for _, r := range rels[1:] {
		if c := maxVersionCode(r.VersionCodes); c > bestCode {
			best, bestCode = r, c
		}
	}
	return best, true
}

// maxVersionCode returns the largest numeric version code in codes, or -1
// when none parse (e.g. a release with no version codes), so any release
// carrying a real code outranks one that carries none.
func maxVersionCode(codes []string) int64 {
	max := int64(-1)
	for _, c := range codes {
		if n, err := strconv.ParseInt(c, 10, 64); err == nil && n > max {
			max = n
		}
	}
	return max
}

// Canonical --columns keys. track and kind are gplay-derived; the rest
// name the top release's fields (matching the API vocabulary per
// ADR-0003 so an operator who has seen the JSON can name the same
// columns).
const (
	colTrack        = "track"
	colKind         = "kind"
	colRelease      = "release"
	colStatus       = "status"
	colUserFraction = "userFraction"
	colVersionCodes = "versionCodes"
)

// DefaultColumns is the table/markdown column order applied when
// --columns is unset. Documented in the command's --help.
var DefaultColumns = []string{colTrack, colKind, colRelease, colStatus, colUserFraction, colVersionCodes}

// columnDef pairs a column's header with the extractor that turns a row
// into that column's cell value.
type columnDef struct {
	header string
	value  func(TrackRow) string
}

// columnRegistry is the single source of truth for which columns exist.
var columnRegistry = map[string]columnDef{
	colTrack:        {"TRACK", func(r TrackRow) string { return r.Track }},
	colKind:         {"KIND", func(r TrackRow) string { return r.Kind }},
	colRelease:      {"RELEASE", func(r TrackRow) string { return r.Release }},
	colStatus:       {"STATUS", func(r TrackRow) string { return r.Status }},
	colUserFraction: {"USER_FRACTION", func(r TrackRow) string { return formatUserFraction(r) }},
	colVersionCodes: {"VERSION_CODES", func(r TrackRow) string { return strings.Join(r.VersionCodes, ",") }},
}

// formatUserFraction renders a release's rollout fraction as a percent,
// but leaves it blank where the number carries no meaning: a track with
// no release, a completed release (already at 100% — the rollout is
// over), and a draft (not yet live, so the fraction is irrelevant).
func formatUserFraction(r TrackRow) string {
	if !r.HasRelease {
		return ""
	}
	switch r.Status {
	case "completed", "draft":
		return ""
	}
	return formatPercent(r.UserFraction)
}

// formatPercent renders a 0..1 fraction as a trimmed percentage: 0.1 ->
// "10%", 0.005 -> "0.5%". It formats at four decimal places (covering
// Play's rollout granularity) then trims trailing zeros, sidestepping the
// float noise of a naive f*100 (0.1*100 == 10.000000000000002).
func formatPercent(f float64) string {
	s := strconv.FormatFloat(f*100, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s + "%"
}

// Payload satisfies output.Renderable. Raw carries the edits.tracks.list
// body for the ADR-0003 JSON pass-through; Rows is the synthesized
// one-line-per-track view and Columns the resolved display order
// (validated in Run, so renderers can trust every key).
type Payload struct {
	Raw     json.RawMessage `json:"-"`
	Rows    []TrackRow      `json:"-"`
	Columns []string        `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format. The
// JSON form is the ADR-0003 edits.tracks.list pass-through; table and
// markdown are human-shaped, one-row-per-track views.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// headers returns the selected columns' display headers, in order.
func (p Payload) headers() []string {
	h := make([]string, len(p.Columns))
	for i, k := range p.Columns {
		h[i] = columnRegistry[k].header
	}
	return h
}

// row extracts the selected columns' cells for one track. An unknown key
// (possible only via a directly-constructed Payload — Run validates
// --columns) renders an empty cell rather than calling a nil value func.
func (p Payload) row(r TrackRow) []string {
	cells := make([]string, len(p.Columns))
	for i, k := range p.Columns {
		if def, ok := columnRegistry[k]; ok {
			cells[i] = def.value(r)
		}
	}
	return cells
}

// renderTable writes a tab-aligned table of the selected columns, one row
// per track.
func renderTable(w io.Writer, p Payload) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(p.headers(), "\t")); err != nil {
		return err
	}
	for _, r := range p.Rows {
		if _, err := fmt.Fprintln(tw, strings.Join(p.row(r), "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// renderJSON emits the raw edits.tracks.list body verbatim (ADR-0003
// pass-through). Raw is always populated on the Run path; an empty Raw
// would mean we never captured the API body, so we error rather than
// synthesize a body (every Payload field is json:"-", so falling back to
// output.WriteJSON would emit a bare "{}" and silently break the contract).
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw tracks.list payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderMarkdown writes the selected columns as a GitHub-Flavored
// Markdown table via output.MarkdownTable.
func renderMarkdown(w io.Writer, p Payload) error {
	rows := make([][]string, 0, len(p.Rows))
	for _, r := range p.Rows {
		rows = append(rows, p.row(r))
	}
	return output.MarkdownTable(w, p.headers(), rows)
}

// resolveColumns turns the --columns spec into a validated, ordered list
// of canonical column keys. An empty spec yields DefaultColumns; an
// unknown key is a CLI misuse (exit 2).
func resolveColumns(spec string) ([]string, error) {
	if strings.TrimSpace(spec) == "" {
		return DefaultColumns, nil
	}
	parts := strings.Split(spec, ",")
	cols := make([]string, 0, len(parts))
	for _, p := range parts {
		k := strings.TrimSpace(p)
		if k == "" {
			continue
		}
		if _, ok := columnRegistry[k]; !ok {
			return nil, &usageError{msg: fmt.Sprintf("unknown column %q (valid: %s)", k, strings.Join(DefaultColumns, ", "))}
		}
		cols = append(cols, k)
	}
	if len(cols) == 0 {
		return nil, &usageError{msg: "no valid columns in --columns"}
	}
	return cols, nil
}

// Run is the business function the kernel invokes. It resolves the
// package, builds an authenticated HTTP client, then opens a read-only
// Edit and lists every track.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}

	cols, err := resolveColumns(in.Columns)
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	var (
		parsed []tracks.Track
		raw    json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		ts, r, e := tracks.List(rc.Ctx, httpClient, pkg, editID)
		if e != nil {
			return e
		}
		parsed, raw = ts, r
		return nil
	}); err != nil {
		return nil, classifyEditError(pkg, err)
	}

	return Payload{Raw: raw, Rows: BuildRows(parsed), Columns: cols}, nil
}

// NewCommand returns the cobra command for `gplay tracks list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every track configured for a package",
		Long: `List every track configured for --package: the four standard tracks
(internal, alpha, beta, production) plus any custom closed tracks the
service account can see. Each row summarizes the track's top release (the
highest version code on it); a standard track that has never been used
still appears, with empty release columns.

Reads the tracks inside a read-only Edit (open → tracks.list → discard);
nothing is committed. Single-track listing of every coexisting release is
the job of ` + "`gplay releases list --track <T>`" + `.

Default table columns: track, kind, release, status, userFraction,
versionCodes. Override with --columns track,status,...  (--output json is
the raw tracks.list payload; --output markdown renders a Markdown table.)`,
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
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: track,kind,release,status,userFraction,versionCodes)")
	return cmd
}
