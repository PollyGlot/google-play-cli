// Package status implements `gplay tracks status`: a read-only, deep
// view of a single track tuned for the "is something wrong right now?"
// question. It lists every release coexisting on --track (draft,
// inProgress, halted, completed) one row each, and makes a halted
// rollout visually unmissable. It is thin glue — resolve --package, open
// a read-only Edit, consume internal/play/tracks.Get, and render. The
// Edit is opened and discarded via edits.WithReadOnlyEdit, never
// committed.
package status

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

// forbiddenError wraps a 403 (service account not invited on the app)
// with the standard grant-access hint. It carries no ExitCode of its own
// so the wrapped *api.Error (403 → exit 11) stays authoritative.
type forbiddenError struct {
	pkg   string
	cause error
}

// Error renders the forbidden message plus the Play Console grant hint.
func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q — in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.pkg, e.cause)
}

// Unwrap exposes the underlying *api.Error so the Coder chain keeps
// mapping the 403 to exit 11.
func (e *forbiddenError) Unwrap() error { return e.cause }

// packageNotFoundError wraps an edits.insert 404 (the package is unknown
// or not registered) with a hint pointing at `gplay apps list`. Like
// trackNotFoundError, it carries no ExitCode of its own so the wrapped
// *api.Error (404 → exit 30) stays authoritative through the Coder chain.
// It mirrors the sibling `gplay tracks list`, so a typo'd --package gives
// the same actionable error on both commands.
type packageNotFoundError struct {
	pkg   string
	cause error
}

// Error renders the not-found message plus the `gplay apps list` hint.
func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found — run `gplay apps list` to see the packages registered with gplay: %v", e.pkg, e.cause)
}

// Unwrap exposes the underlying *api.Error so the Coder chain keeps
// mapping the 404 to exit 30.
func (e *packageNotFoundError) Unwrap() error { return e.cause }

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

// columns is the single source of truth for the status table: its
// declaration order is both the set of valid --columns keys and the default
// column order. The shared output.ColumnSet owns selection and the
// table/markdown rendering; the only status-specific cell is the halted
// marker carried by the status column's Value (see markStatus and
// docs/adr/0018-shared-list-table-machinery.md).
var columns = output.NewColumnSet(
	output.Column[tracks.Release]{Key: colName, Header: "NAME", Value: func(r tracks.Release) string { return r.Name }},
	output.Column[tracks.Release]{Key: colStatus, Header: "STATUS", Value: func(r tracks.Release) string { return markStatus(r.Status) }},
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

// standardTracks are the four well-known tracks Google Play provisions for
// every app. Any other name is a custom closed track. The classification
// is gplay-derived — the tracks.get payload has no such field — so it
// lives only in the human views, never the JSON pass-through.
var standardTracks = map[string]bool{
	"internal":   true,
	"alpha":      true,
	"beta":       true,
	"production": true,
}

// deriveKind labels a track standard vs custom from its name.
func deriveKind(track string) string {
	if standardTracks[track] {
		return "standard"
	}
	return "custom"
}

// markStatus makes a halted rollout unmissable: `halted` renders
// uppercased and prefixed with `!` (-> !HALTED), so the operator scanning
// the table spots it at a glance. Every other status is left in its plain
// API form, so the marker keeps meaning exactly "this rollout is halted".
func markStatus(s string) string {
	if s == "halted" {
		return "!" + strings.ToUpper(s)
	}
	return s
}

// Payload satisfies output.Renderable. Raw carries the tracks.get body
// for the ADR-0003 JSON pass-through; Columns is the resolved display
// order (validated in Run, so renderers can trust every key). Track and
// Kind are gplay-derived context shown above the table — never in the
// JSON, which stays a faithful tracks.get pass-through.
type Payload struct {
	Track    string                          `json:"-"`
	Kind     string                          `json:"-"`
	Releases []tracks.Release                `json:"-"`
	Raw      json.RawMessage                 `json:"-"`
	Columns  []output.Column[tracks.Release] `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format.
// The JSON form is the ADR-0003 tracks.get pass-through; table and
// markdown are human-shaped views over the same releases drawn by the
// shared column helper, each preceded by a track-context line.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// renderTable writes a one-line track-context header (track name + the
// derived standard/custom kind) then the shared column table, one row per
// release on the track. The header is the "which track, and is it a real
// one" anchor for the at-a-glance read; it is omitted when Track is unset
// (a directly-constructed Payload).
func renderTable(w io.Writer, p Payload) error {
	if p.Track != "" {
		if _, err := fmt.Fprintf(w, "Track: %s (%s)\n", p.Track, p.Kind); err != nil {
			return err
		}
	}
	return output.RenderTable(w, p.Columns, p.Releases)
}

// renderJSON emits the raw tracks.get body verbatim (ADR-0003
// pass-through). Raw is always populated on the Run path; an empty Raw
// would mean we never captured the API body, so we error rather than
// emit zero bytes (every Payload field is json:"-", so an empty Raw
// would silently break the contract).
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw tracks.get payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderMarkdown writes the shared column table as GitHub-Flavored
// Markdown, preceded by the same track-context line as the table view so a
// pasted report stands on its own without the originating command line. The
// halted marker carries through the status column's extractor, so a halted
// rollout stands out in markdown too.
func renderMarkdown(w io.Writer, p Payload) error {
	if p.Track != "" {
		if _, err := fmt.Fprintf(w, "Track: %s (%s)\n\n", p.Track, p.Kind); err != nil {
			return err
		}
	}
	return output.RenderMarkdown(w, p.Columns, p.Releases)
}

// isStatus reports whether err carries a *api.Error with the given HTTP
// status — the signal used to attach actionable hints to a tracks.get 404
// (unknown track) or a 403 (service account not invited).
func isStatus(err error, status int) bool {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == status
	}
	return false
}

// classifyEditError attaches an actionable hint to the operator-facing
// failures of a read-only status read, while leaving the wrapped
// *api.Error to drive the exit code. A tracks.get 404 is already wrapped
// as *trackNotFoundError inside the read closure (it carries the
// `gplay tracks list` hint), so it is passed through untouched rather than
// being re-classified as a package miss. Everything else is matched on the
// underlying *api.Error: an edits.insert 404 means the package is unknown
// (→ `gplay apps list` hint, mirroring `tracks list`), a 403 means the
// service account was not invited on the app. Every other failure (5xx,
// network, edit conflict) propagates verbatim.
func classifyEditError(pkg string, err error) error {
	var tnf *trackNotFoundError
	if errors.As(err, &tnf) {
		return err
	}
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

// Run is the business function the kernel invokes. It validates inputs,
// resolves the package, builds an authenticated HTTP client, then opens a
// read-only Edit and reads the single track.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	in.Track = strings.TrimSpace(in.Track)
	if in.Track == "" {
		return nil, exit.Usagef("missing --track")
	}

	pkg := strings.TrimSpace(in.Package)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
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

	var (
		parsed *tracks.Track
		raw    json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		p, r, e := tracks.Get(rc.Ctx, httpClient, pkg, editID, in.Track)
		if e != nil {
			if isStatus(e, http.StatusNotFound) {
				return &trackNotFoundError{track: in.Track, cause: e}
			}
			return e
		}
		parsed, raw = p, r
		return nil
	}); err != nil {
		return nil, classifyEditError(pkg, err)
	}

	return Payload{
		Track:    in.Track,
		Kind:     deriveKind(in.Track),
		Releases: parsed.Releases,
		Raw:      raw,
		Columns:  cols,
	}, nil
}

// NewCommand returns the cobra command for `gplay tracks status`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the full state of a single track (is anything wrong right now?)",
		Long: `Show the full state of --track: every release coexisting on it
(draft, inProgress, halted, completed) one row each, with the track's
derived kind (standard or custom). A halted rollout is rendered as
!HALTED so it is impossible to miss at a glance.

Reads the track inside a read-only Edit (open → tracks.get → discard);
nothing is committed. Cross-track listing is the job of ` + "`gplay tracks list`" + `;
mutating verbs (promote, rollout, halt, resume) live under ` + "`gplay releases`" + `.

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
	cmd.Flags().StringVar(&in.Track, "track", "", "track to inspect (internal, alpha, beta, production, or any closed-track name)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+strings.Join(columns.DefaultKeys(), ",")+")")
	return cmd
}
