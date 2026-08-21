// Package list implements `gplay testers list`: a read-only view of the
// authorized audience (Google Groups) of a single track. It is thin glue:
// resolve --package, open a read-only Edit, consume
// internal/play/testers.Get, and render. The Edit is opened and discarded
// via edits.WithReadOnlyEdit, never committed.
//
// The testers resource exposes only googleGroups[]; individual tester
// emails are not supported by the API, so this lists Google Groups
// exclusively. Replacing the audience is the job of `gplay testers set`.
package list

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/testers"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Track   string
}

// usageError is a CLI-misuse error (missing --track, no package);
// ExitCode()=2 per docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func (e *usageError) ExitCode() int { return 2 }

// trackNotFoundError wraps a testers.get 404 with an actionable hint
// pointing at `gplay tracks list`. It carries no ExitCode of its own so
// the wrapped *api.Error (404 → exit 30) stays authoritative through the
// Coder chain: an unknown track is an API 4xx, not a CLI misuse.
type trackNotFoundError struct {
	track string
	cause error
}

// Error renders the not-found message plus the `gplay tracks list` hint.
func (e *trackNotFoundError) Error() string {
	return fmt.Sprintf("track %q not found: run `gplay tracks list` to see the tracks configured for this app: %v", e.track, e.cause)
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
	return fmt.Sprintf("service account is not granted access to %q: in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.pkg, e.cause)
}

// Unwrap exposes the underlying *api.Error so the Coder chain keeps
// mapping the 403 to exit 11.
func (e *forbiddenError) Unwrap() error { return e.cause }

// packageNotFoundError wraps an edits.insert 404 (the package is unknown
// or not registered) with a hint pointing at `gplay apps list`. Like
// trackNotFoundError, it carries no ExitCode of its own so the wrapped
// *api.Error (404 → exit 30) stays authoritative through the Coder chain.
type packageNotFoundError struct {
	pkg   string
	cause error
}

// Error renders the not-found message plus the `gplay apps list` hint.
func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found: run `gplay apps list` to see the packages registered with gplay: %v", e.pkg, e.cause)
}

// Unwrap exposes the underlying *api.Error so the Coder chain keeps
// mapping the 404 to exit 30.
func (e *packageNotFoundError) Unwrap() error { return e.cause }

// Payload satisfies output.Renderable. Raw carries the testers.get body
// for the ADR-0003 JSON pass-through; Track is gplay-derived context shown
// above the group list: never in the JSON, which stays a faithful
// testers.get pass-through. Groups is the parsed googleGroups[] used by the
// human views.
type Payload struct {
	Track  string          `json:"-"`
	Groups []string        `json:"-"`
	Raw    json.RawMessage `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format. The
// JSON form is the ADR-0003 testers.get pass-through; table and markdown
// are human-shaped views over the same Google Groups.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// renderTable writes a one-line track-context header then one Google Group
// per line under a GROUP header. An empty audience (a closed track with no
// testers yet) renders a `(no testers)` line so the empty state reads
// unambiguously rather than looking like a truncated table.
func renderTable(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "Track: %s\n", p.Track); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "GROUP"); err != nil {
		return err
	}
	if len(p.Groups) == 0 {
		if _, err := fmt.Fprintln(tw, "(no testers)"); err != nil {
			return err
		}
	}
	for _, g := range p.Groups {
		if _, err := fmt.Fprintln(tw, g); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// renderJSON emits the raw testers.get body verbatim (ADR-0003
// pass-through). Raw is always populated on the Run path; an empty Raw
// would mean we never captured the API body, so we error rather than emit
// zero bytes (every Payload field is json:"-", so an empty Raw would
// silently break the contract).
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw testers.get payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderMarkdown writes the Google Groups as a GitHub-Flavored Markdown
// table via output.MarkdownTable, preceded by the same track-context line
// as the table view so a pasted report stands on its own. An empty audience
// yields a header-only table (zero rows).
func renderMarkdown(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "Track: %s\n\n", p.Track); err != nil {
		return err
	}
	rows := make([][]string, 0, len(p.Groups))
	for _, g := range p.Groups {
		rows = append(rows, []string{g})
	}
	return output.MarkdownTable(w, []string{"GROUP"}, rows)
}

// isStatus reports whether err carries a *api.Error with the given HTTP
// status: the signal used to attach actionable hints to a testers.get 404
// (unknown track) or a 403 (service account not invited).
func isStatus(err error, status int) bool {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == status
	}
	return false
}

// classifyEditError attaches an actionable hint to the operator-facing
// failures of a read-only testers read, while leaving the wrapped
// *api.Error to drive the exit code. A testers.get 404 is already wrapped
// as *trackNotFoundError inside the read closure (it carries the
// `gplay tracks list` hint), so it is passed through untouched rather than
// being re-classified as a package miss. Everything else is matched on the
// underlying *api.Error: an edits.insert 404 means the package is unknown
// (→ `gplay apps list` hint), a 403 means the service account was not
// invited on the app. Every other failure (5xx, network, edit conflict)
// propagates verbatim.
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
// read-only Edit and reads the track's testers.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	in.Track = strings.TrimSpace(in.Track)
	if in.Track == "" {
		return nil, &usageError{msg: "missing --track"}
	}

	pkg := strings.TrimSpace(in.Package)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	var (
		parsed *testers.Testers
		raw    json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		tt, r, e := testers.Get(rc.Ctx, httpClient, pkg, editID, in.Track)
		if e != nil {
			if isStatus(e, http.StatusNotFound) {
				return &trackNotFoundError{track: in.Track, cause: e}
			}
			return e
		}
		parsed, raw = tt, r
		return nil
	}); err != nil {
		return nil, classifyEditError(pkg, err)
	}

	return Payload{
		Track:  in.Track,
		Groups: parsed.GoogleGroups,
		Raw:    raw,
	}, nil
}

// NewCommand returns the cobra command for `gplay testers list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the Google Groups authorized to test a track",
		Long: `List the authorized audience of --track: the Google Groups that may
test it. The testers resource exposes only Google Groups: individual
tester emails are not supported by the API, so this lists groups
exclusively.

Reads the audience inside a read-only Edit (open → testers.get → discard);
nothing is committed. Replacing the audience is the job of ` + "`gplay testers set`" + `.

--output json is the raw testers.get payload (ADR-0003); --output markdown
renders a Markdown table.`,
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
	cmd.Flags().StringVar(&in.Track, "track", "", "track whose testers to list (any closed-track name)")
	return cmd
}
