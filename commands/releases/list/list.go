// Package list implements `gplay releases list`: a read-only listing of
// every release currently attached to a track (draft, inProgress,
// halted, completed). It is thin glue — resolve --package, open a
// read-only Edit, consume internal/play/tracks.Get, and render. No
// orchestrator, no mutation: the Edit is opened and discarded via
// edits.WithReadOnlyEdit, never committed.
//
// Cross-track listing is the job of `gplay tracks list` (#5); this
// command always takes --track.
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
	Track   string
	Columns string
}

// usageError is a CLI-misuse error (missing --track, no package, unknown
// column); ExitCode()=2 per docs/DESIGN.md §9.
type usageError struct{ msg string }

// Error returns the misuse message.
func (e *usageError) Error() string { return e.msg }

// ExitCode reports 2 (CLI misuse) per docs/DESIGN.md §9.
func (e *usageError) ExitCode() int { return 2 }

// trackNotFoundError wraps a tracks.get 404 with an actionable hint
// pointing at `gplay tracks list`. It carries no ExitCode of its own so
// the wrapped *api.Error (404 → exit 30) stays authoritative through the
// Coder chain — an unknown track is an API 4xx, not a CLI misuse.
type trackNotFoundError struct {
	track string
	cause error
}

// Error renders the not-found message plus the `gplay tracks list` hint.
func (e *trackNotFoundError) Error() string {
	return fmt.Sprintf("track %q not found — run `gplay tracks list` to see the tracks configured for this app: %v", e.track, e.cause)
}

// Unwrap exposes the underlying *api.Error so the Coder chain keeps
// mapping the 404 to exit 30.
func (e *trackNotFoundError) Unwrap() error { return e.cause }

// Canonical --columns keys. They match the API field names (per ADR-0003
// the JSON view is pass-through) so an operator who has seen the JSON can
// name the same columns for the table view.
const (
	colName         = "name"
	colStatus       = "status"
	colUserFraction = "userFraction"
	colVersionCodes = "versionCodes"
	colNotes        = "notes"
)

// DefaultColumns is the table/markdown column order applied when
// --columns is unset. Documented in the command's --help.
var DefaultColumns = []string{colName, colStatus, colUserFraction, colVersionCodes, colNotes}

// columnDef pairs a column's table/markdown header with the extractor
// that turns a release into that column's cell value.
type columnDef struct {
	header string
	value  func(tracks.Release) string
}

// columnRegistry is the single source of truth for which columns exist:
// it maps each canonical --columns key to its header and cell extractor.
// resolveColumns validates user input against it, and the renderers read
// headers and values from it.
var columnRegistry = map[string]columnDef{
	colName:         {"NAME", func(r tracks.Release) string { return r.Name }},
	colStatus:       {"STATUS", func(r tracks.Release) string { return r.Status }},
	colUserFraction: {"USER_FRACTION", func(r tracks.Release) string { return formatFraction(r.UserFraction) }},
	colVersionCodes: {"VERSION_CODES", func(r tracks.Release) string { return strings.Join(r.VersionCodes, ",") }},
	colNotes:        {"NOTES_LOCALES", func(r tracks.Release) string { return strconv.Itoa(len(r.ReleaseNotes)) }},
}

// formatFraction renders a userFraction without trailing zeros (0.1, 0.5,
// 1, 0). 'f' with -1 precision gives the shortest decimal form; unlike
// 'g' it never switches to scientific notation, so a small staged
// fraction shows as e.g. 0.001 rather than 1e-03.
func formatFraction(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// Payload satisfies output.Renderable. Raw carries the tracks.get body
// for the ADR-0003 JSON pass-through; Columns is the resolved display
// order (validated in Run, so renderers can trust every key).
type Payload struct {
	Track    string           `json:"track"`
	Releases []tracks.Release `json:"releases"`
	Raw      json.RawMessage  `json:"-"`
	Columns  []string         `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format.
// The JSON form is the ADR-0003 tracks.get pass-through; table and
// markdown are human-shaped views over the same releases.
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

func (p Payload) row(r tracks.Release) []string {
	cells := make([]string, len(p.Columns))
	for i, k := range p.Columns {
		// Run validates every key against columnRegistry, so on the
		// command path def is always present. Guard anyway: Payload is
		// exported, and an unknown key would otherwise call a nil value
		// func and panic. An unknown key renders an empty cell, keeping
		// header/row widths aligned (headers() reads the same zero def).
		if def, ok := columnRegistry[k]; ok {
			cells[i] = def.value(r)
		}
	}
	return cells
}

// renderTable writes a tab-aligned table of the selected columns, or a
// friendly note when the track carries no releases.
func renderTable(w io.Writer, p Payload) error {
	if len(p.Releases) == 0 {
		_, err := fmt.Fprintf(w, "(no releases on track %s)\n", p.Track)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(p.headers(), "\t")); err != nil {
		return err
	}
	for _, r := range p.Releases {
		if _, err := fmt.Fprintln(tw, strings.Join(p.row(r), "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// renderJSON emits the raw tracks.get body verbatim (ADR-0003
// pass-through), falling back to the gplay Payload shape only if the raw
// body is somehow absent.
func renderJSON(w io.Writer, p Payload) error {
	// API pass-through: emit the raw tracks.get body (ADR-0003).
	if len(p.Raw) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, p)
}

// renderMarkdown writes the selected columns as a GitHub-Flavored
// Markdown table via output.MarkdownTable.
func renderMarkdown(w io.Writer, p Payload) error {
	rows := make([][]string, 0, len(p.Releases))
	for _, r := range p.Releases {
		rows = append(rows, p.row(r))
	}
	return output.MarkdownTable(w, p.headers(), rows)
}

// Run is the business function the kernel invokes. It validates inputs,
// resolves the package, builds an authenticated HTTP client, then opens a
// read-only Edit and reads the track. There is no dry-run path: a listing
// must hit the API, so a resolved Account is always required.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.Track == "" {
		return nil, &usageError{msg: "missing --track"}
	}

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

	var parsed *tracks.Track
	var raw json.RawMessage
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		p, r, e := tracks.Get(rc.Ctx, httpClient, pkg, editID, in.Track)
		if e != nil {
			if isTrackNotFound(e) {
				return &trackNotFoundError{track: in.Track, cause: e}
			}
			return e
		}
		parsed, raw = p, r
		return nil
	}); err != nil {
		return nil, err
	}

	return Payload{
		Track:    parsed.Track,
		Releases: parsed.Releases,
		Raw:      raw,
		Columns:  cols,
	}, nil
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

// isTrackNotFound reports whether err is a tracks.get 404 — the signal
// that the requested track does not exist on this app.
func isTrackNotFound(err error) bool {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

// NewCommand returns the cobra command for `gplay releases list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every release currently on a track",
		Long: `List every release currently attached to --track, including any
draft, inProgress, halted, or completed entries that coexist on it.

Reads the track inside a read-only Edit (open → tracks.get → discard);
nothing is committed. Cross-track listing is the job of ` + "`gplay tracks list`" + `.

Default table columns: name, status, userFraction, versionCodes, notes.
Override with --columns name,status,...  (--output json is the raw
tracks.get payload; --output markdown renders a Markdown table.)`,
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
	cmd.Flags().StringVar(&in.Track, "track", "", "track to list releases from (internal, alpha, beta, production, or any closed-track name)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: name,status,userFraction,versionCodes,notes)")
	return cmd
}
