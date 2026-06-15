// Package set implements `gplay testers set`: it replaces the authorized
// audience (Google Groups) of a single track, declaratively — the whole
// list is sent, mapping 1:1 to edits.testers.update (there is no
// add/remove). It is a write: an implicit Edit (open → testers.update →
// commit), with --dry-run (validate + preview, no HTTP) and
// --keep-edit-on-failure, matching `releases upload` / `promote`. There is
// NO --confirm: a closed test track is low-stakes and reversible.
//
// The footgun guard is the key behavior: a bare `set` with neither --group
// nor --clear is a misuse (exit 2), so a forgotten --group can never
// silently wipe the list; emptying the audience on purpose is the explicit
// --clear.
package set

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

// Input is the request-shaped struct cobra builds from flags. GroupsSet
// records whether --group was passed at all (cmd.Flags().Changed), so the
// footgun guard can tell "no --group" from "--group with an empty value".
type Input struct {
	Package           string
	Track             string
	Groups            []string
	GroupsSet         bool
	Clear             bool
	DryRun            bool
	KeepEditOnFailure bool
}

// usageError is a CLI-misuse error (missing --track, no package, the
// footgun guard, --group/--clear conflict); ExitCode()=2 per
// docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func (e *usageError) ExitCode() int { return 2 }

// trackNotFoundError wraps a testers.update 404 with an actionable hint
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

// Payload satisfies output.Renderable. Raw carries the testers.update body
// for the ADR-0003 JSON pass-through (empty on --dry-run, which never hits
// the API); Track and DryRun are gplay-derived context shown above the
// group list — never in the JSON, which stays a faithful testers.update
// pass-through. Groups is the audience that was (or would be) written.
type Payload struct {
	Track  string          `json:"-"`
	Groups []string        `json:"-"`
	Raw    json.RawMessage `json:"-"`
	DryRun bool            `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format. The
// JSON form is the ADR-0003 testers.update pass-through; table and markdown
// are human-shaped views over the same Google Groups.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// renderTable writes a one-line track-context header (plus a `(dry-run)`
// marker when nothing was written) then one Google Group per line under a
// GROUP header. An empty audience (a --clear, or a track with no testers)
// renders a `(no testers)` line so the empty state reads unambiguously.
func renderTable(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "Track: %s%s\n", p.Track, dryRunSuffix(p.DryRun)); err != nil {
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

// renderJSON emits the raw testers.update body verbatim (ADR-0003
// pass-through). On --dry-run no API call was made, so Raw is empty; we
// error rather than emit zero bytes so the contract break is loud (a
// dry-run preview belongs to the table/markdown views, not the JSON
// pass-through).
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw testers.update payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderMarkdown writes the Google Groups as a GitHub-Flavored Markdown
// table via output.MarkdownTable, preceded by the same track-context line
// (with the dry-run marker) as the table view so a pasted report stands on
// its own. An empty audience yields a header-only table (zero rows).
func renderMarkdown(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "Track: %s%s\n\n", p.Track, dryRunSuffix(p.DryRun)); err != nil {
		return err
	}
	rows := make([][]string, 0, len(p.Groups))
	for _, g := range p.Groups {
		rows = append(rows, []string{g})
	}
	return output.MarkdownTable(w, []string{"GROUP"}, rows)
}

// dryRunSuffix returns the ` (dry-run)` marker appended to the track-context
// line when nothing was written, so a previewed payload is never mistaken
// for an applied one.
func dryRunSuffix(dryRun bool) string {
	if dryRun {
		return " (dry-run)"
	}
	return ""
}

// isStatus reports whether err carries a *api.Error with the given HTTP
// status — the signal used to attach actionable hints to a testers.update
// 404 (unknown track) or a 403 (service account not invited).
func isStatus(err error, status int) bool {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == status
	}
	return false
}

// classifyEditError attaches an actionable hint to the operator-facing
// failures of a testers replacement, while leaving the wrapped *api.Error
// to drive the exit code. A testers.update 404 is already wrapped as
// *trackNotFoundError inside the write closure (it carries the
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

// targetGroups resolves the audience to write from the validated flags.
// --clear yields an empty (non-nil) set; otherwise the trimmed, non-empty
// entries of --group. The caller has already run the footgun and conflict
// guards, so this only normalizes — it does not re-validate that a set was
// requested.
func targetGroups(in Input) []string {
	if in.Clear {
		return []string{}
	}
	groups := make([]string, 0, len(in.Groups))
	for _, g := range in.Groups {
		if g = strings.TrimSpace(g); g != "" {
			groups = append(groups, g)
		}
	}
	return groups
}

// Run is the business function the kernel invokes. It validates the flag
// combination (notably the footgun guard), resolves the package, and —
// unless --dry-run short-circuits before any network — opens an Edit,
// replaces the track's testers, and commits.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	in.Track = strings.TrimSpace(in.Track)
	if in.Track == "" {
		return nil, &usageError{msg: "missing --track"}
	}

	// Footgun guard: a bare `set` with neither --group nor --clear must
	// never reach the API — a forgotten --group would otherwise silently
	// wipe the list. Emptying on purpose is the explicit --clear.
	if !in.GroupsSet && !in.Clear {
		return nil, &usageError{msg: "refusing to change testers without --group or --clear (a forgotten --group must not silently wipe the list); pass --group a@googlegroups.com[,…] to declare the set, or --clear to empty it"}
	}
	if in.GroupsSet && in.Clear {
		return nil, &usageError{msg: "--group and --clear are mutually exclusive"}
	}

	groups := targetGroups(in)
	// --group was given but every entry blanked out after trimming: don't
	// send an empty set under the guise of --group (that's what --clear is
	// for).
	if in.GroupsSet && len(groups) == 0 {
		return nil, &usageError{msg: "--group was passed but lists no non-empty group; pass --group a@googlegroups.com[,…], or --clear to empty the list"}
	}

	pkg := strings.TrimSpace(in.Package)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}

	// Dry-run skips auth entirely: nothing hits the network, so a missing
	// Account is not a problem here. We preview the exact audience that
	// would be written (empty for --clear) without opening an Edit.
	if in.DryRun {
		return Payload{
			Track:  in.Track,
			Groups: groups,
			DryRun: true,
		}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	var (
		parsed *testers.Testers
		raw    json.RawMessage
	)
	if err := edits.WithEdit(rc.Ctx, httpClient, pkg, edits.Options{KeepOnFailure: in.KeepEditOnFailure}, func(editID string) error {
		tt, r, e := testers.Update(rc.Ctx, httpClient, pkg, editID, in.Track, groups)
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

	// DESIGN §8: a committed mutation prints one ✓ line on stderr. The
	// --dry-run path returned above, so this only runs after a real update.
	rc.Confirmf("testers set on track %q (%d group(s))", in.Track, len(parsed.GoogleGroups))
	return Payload{
		Track:  in.Track,
		Groups: parsed.GoogleGroups,
		Raw:    raw,
	}, nil
}

// NewCommand returns the cobra command for `gplay testers set`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Replace the Google Groups authorized to test a track",
		Long: `Replace the authorized audience of --track with the Google Groups passed
to --group. testers set is declarative: it replaces the WHOLE list (it
maps 1:1 to testers.update — there is no add/remove). The testers resource
exposes only Google Groups; individual tester emails are not supported by
the API.

A bare ` + "`set`" + ` with neither --group nor --clear is refused (exit 2) so a
forgotten --group can never silently wipe the list; empty the audience on
purpose with --clear. There is no --confirm: a closed test track is
low-stakes and reversible.

Writes inside an implicit Edit (open → testers.update → commit). Use
--dry-run to validate and preview the payload without any HTTP call, and
--keep-edit-on-failure to skip the auto-discard cleanup for debugging.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.GroupsSet = cmd.Flags().Changed("group")
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Track, "track", "", "track whose testers to replace (any closed-track name)")
	cmd.Flags().StringSliceVar(&in.Groups, "group", nil, "Google Group email(s) authorized to test (repeatable or comma-separated)")
	cmd.Flags().BoolVar(&in.Clear, "clear", false, "replace the tester list with an empty set (close the closed test)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the tester list without any HTTP call")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	return cmd
}
