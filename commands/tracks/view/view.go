package view

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

type Input struct {
	Package string
	Track   string
	Columns string
}

type trackNotFoundError struct {
	track string
	cause error
}

func (e *trackNotFoundError) Error() string {
	return fmt.Sprintf("track %q not found: run `gplay tracks list` to see the tracks configured for this app: %v", e.track, e.cause)
}

func (e *trackNotFoundError) Unwrap() error { return e.cause }

const (
	colName         = "name"
	colStatus       = "status"
	colUserFraction = "userFraction"
	colVersionCodes = "versionCodes"
	colNotes        = "notes"
)

var columns = output.NewColumnSet(
	output.Column[tracks.Release]{Key: colName, Header: "NAME", Value: func(r tracks.Release) string { return r.Name }},
	output.Column[tracks.Release]{Key: colStatus, Header: "STATUS", Value: func(r tracks.Release) string { return markStatus(r.Status) }},
	output.Column[tracks.Release]{Key: colUserFraction, Header: "USER_FRACTION", Value: func(r tracks.Release) string { return formatFraction(r.UserFraction) }},
	output.Column[tracks.Release]{Key: colVersionCodes, Header: "VERSION_CODES", Value: func(r tracks.Release) string { return strings.Join(r.VersionCodes, ",") }},
	output.Column[tracks.Release]{Key: colNotes, Header: "NOTES_LOCALES", Value: func(r tracks.Release) string { return strconv.Itoa(len(r.ReleaseNotes)) }},
)

func ResolveColumns(spec string) ([]output.Column[tracks.Release], error) {
	return columns.Resolve(spec)
}

func formatFraction(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

var standardTracks = map[string]bool{
	"internal":   true,
	"alpha":      true,
	"beta":       true,
	"production": true,
}

func deriveKind(track string) string {
	if standardTracks[track] {
		return "standard"
	}
	return "custom"
}

func markStatus(s string) string {
	if s == tracks.StatusHalted {
		return "!" + strings.ToUpper(s)
	}
	return s
}

type Payload struct {
	Track    string                          `json:"-"`
	Kind     string                          `json:"-"`
	Releases []tracks.Release                `json:"-"`
	Raw      json.RawMessage                 `json:"-"`
	Columns  []output.Column[tracks.Release] `json:"-"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func renderTable(w io.Writer, p Payload) error {
	if p.Track != "" {
		if _, err := fmt.Fprintf(w, "Track: %s (%s)\n", p.Track, p.Kind); err != nil {
			return err
		}
	}
	return output.RenderTable(w, p.Columns, p.Releases)
}

func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw tracks.get payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

func renderMarkdown(w io.Writer, p Payload) error {
	if p.Track != "" {
		if _, err := fmt.Fprintf(w, "Track: %s (%s)\n\n", p.Track, p.Kind); err != nil {
			return err
		}
	}
	return output.RenderMarkdown(w, p.Columns, p.Releases)
}

func isStatus(err error, status int) bool {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == status
	}
	return false
}

func classifyTrackError(pkg string, err error) error {
	var tnf *trackNotFoundError
	if errors.As(err, &tnf) {
		return err
	}
	return apihint.ForPackage(pkg, err)
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	in.Track = strings.TrimSpace(in.Track)
	if in.Track == "" {
		return nil, exit.Usagef("missing --track: pass --track <name> (internal, alpha, beta, production, or any closed-track name)")
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
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
		return nil, classifyTrackError(pkg, err)
	}

	return Payload{
		Track:    in.Track,
		Kind:     deriveKind(in.Track),
		Releases: parsed.Releases,
		Raw:      raw,
		Columns:  cols,
	}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view",
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
		Example: `  gplay tracks view --track production
  gplay tracks view --track qa-team --output markdown
  gplay tracks view --track beta --output json`,
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
	cmd.Flags().StringVar(&in.Track, "track", "", "track to inspect (internal, alpha, beta, production, or any closed-track name) (required)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+strings.Join(columns.DefaultKeys(), ",")+")")
	return cmd
}
