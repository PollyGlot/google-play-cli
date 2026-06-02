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

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
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

// columns is the single source of truth for the releases table: its
// declaration order is both the set of valid --columns keys and the default
// column order. The shared output.ColumnSet owns selection and the
// table/markdown rendering (docs/adr/0018-shared-list-table-machinery.md).
var columns = output.NewColumnSet(
	output.Column[tracks.Release]{Key: colName, Header: "NAME", Value: func(r tracks.Release) string { return r.Name }},
	output.Column[tracks.Release]{Key: colStatus, Header: "STATUS", Value: func(r tracks.Release) string { return r.Status }},
	output.Column[tracks.Release]{Key: colUserFraction, Header: "USER_FRACTION", Value: func(r tracks.Release) string { return formatFraction(r.UserFraction) }},
	output.Column[tracks.Release]{Key: colVersionCodes, Header: "VERSION_CODES", Value: func(r tracks.Release) string { return strings.Join(r.VersionCodes, ",") }},
	output.Column[tracks.Release]{Key: colNotes, Header: "NOTES_LOCALES", Value: func(r tracks.Release) string { return strconv.Itoa(len(r.ReleaseNotes)) }},
)

// ResolveColumns turns a --columns spec into the validated, ordered columns
// for a Payload. Run uses it on the command path; it is exported so render
// tests (and any caller building a Payload directly) share the one column
// registry rather than hand-rolling a list that could drift from it.
func ResolveColumns(spec string) ([]output.Column[tracks.Release], error) {
	return columns.Resolve(spec)
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
	Track    string                          `json:"track"`
	Releases []tracks.Release                `json:"releases"`
	Raw      json.RawMessage                 `json:"-"`
	Columns  []output.Column[tracks.Release] `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format.
// The JSON form is the ADR-0003 tracks.get pass-through; table and
// markdown are human-shaped views over the same releases drawn by the
// shared column helper.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Columns, p.Releases) },
	}
}

// renderTable writes a tab-aligned table of the selected columns via the
// shared helper, or a friendly note when the track carries no releases.
func renderTable(w io.Writer, p Payload) error {
	if len(p.Releases) == 0 {
		_, err := fmt.Fprintf(w, "(no releases on track %s)\n", p.Track)
		return err
	}
	return output.RenderTable(w, p.Columns, p.Releases)
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

// Run is the business function the kernel invokes. It validates inputs,
// resolves the package, builds an authenticated HTTP client, then opens a
// read-only Edit and reads the track. There is no dry-run path: a listing
// must hit the API, so a resolved Account is always required.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.Track == "" {
		return nil, exit.Usagef("missing --track")
	}

	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, exit.Usagef("no package — pass --package <pkg> or run gplay init in your repo")
	}

	cols, err := ResolveColumns(in.Columns)
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
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+strings.Join(columns.DefaultKeys(), ",")+")")
	return cmd
}
