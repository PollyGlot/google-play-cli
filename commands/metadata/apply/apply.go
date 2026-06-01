// Package apply implements `gplay metadata apply`: reconcile the local
// Metadata tree with the Listings live on Google Play. It is thin glue —
// resolve --package and --dir, read the tree off disk (internal/metadata/
// tree), and hand it to internal/metadata/orchestrator, which owns the Edit
// lifecycle, the diff, the --confirm gate, and the --prune deletegroup. The
// command only resolves inputs and renders.
//
// Two modes (ADR-0011):
//
//   - --dry-run is ONLINE: it reads the live Listings and prints the
//     per-locale/per-field delta without committing. --output json is the
//     gplay diff schema {package, changes[], summary} so a CI gate is one
//     jq line. This deliberately diverges from `releases upload --dry-run`
//     (offline); the offline role is held by `metadata validate`.
//   - --confirm performs the real publish (one Edit, one commit, atomic).
//     --output json is the per-locale listings.patch response bodies.
//     Without --confirm a real apply refuses (exit 2) and points at
//     --dry-run; CI=true never auto-confirms.
package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/diff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// DefaultDir is the metadata tree root used when --dir is unset. Shared by
// the flag default and Run's fallback so direct callers (tests) get the
// same default.
const DefaultDir = "./metadata"

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package     string
	Dir         string
	DryRun      bool
	Confirm     bool
	Prune       bool
	AllowLocale []string
}

// usageError is a CLI-misuse error (no package); ExitCode()=2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// dirError signals an unreadable --dir (missing tree). It maps to exit 20
// (client-side validation) — the same family as a malformed tree.
type dirError struct {
	dir   string
	cause error
}

func (e *dirError) Error() string {
	return fmt.Sprintf("metadata dir %q is not readable: %v (create it, or run `gplay metadata pull` to seed it)", e.dir, e.cause)
}
func (e *dirError) Unwrap() error { return e.cause }
func (e *dirError) ExitCode() int { return 20 }

// packageNotFoundError / forbiddenError mirror `tracks list` / `metadata
// list`: actionable hints on a 404 / 403, with the wrapped *api.Error left
// to drive the exit code.
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found — run `gplay apps list` to see the packages registered with gplay: %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q — in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.pkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// classifyEditError adds the 404/403 hints, leaving the wrapped *api.Error
// to drive the exit code. The orchestrator's own typed errors
// (ConfirmRequired, Validation, PruneDefaultLanguage) are not *api.Error,
// so they pass through untouched with their own exit codes.
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

// Payload renders an orchestrator.Result. The shape switches on Result.DryRun:
// a dry-run renders the diff (JSON = gplay diff schema), a real apply renders
// what was published (JSON = per-locale patch bodies).
type Payload struct {
	Result *orchestrator.Result
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

// diffHeaders / diffRows shape the dry-run change table (and its markdown
// twin). LIVE/LOCAL are the online/local char counts; a locale-level record
// (untouchedLocale/delete) leaves them blank and fills REASON.
var diffHeaders = []string{"LOCALE", "FIELD", "OP", "LIVE", "LOCAL", "REASON"}

func diffRows(d diff.Result) [][]string {
	rows := make([][]string, 0, len(d.Changes))
	for _, c := range d.Changes {
		rows = append(rows, []string{
			c.Locale, c.Field, string(c.Op),
			ptrStr(c.LiveChars), ptrStr(c.LocalChars), c.Reason,
		})
	}
	return rows
}

func ptrStr(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

// applyHeaders / applyRows shape the real-apply report table: one row per
// locale touched, with the action taken.
var applyHeaders = []string{"LOCALE", "RESULT"}

func applyRows(r *orchestrator.Result) [][]string {
	rows := make([][]string, 0, len(r.Patched)+len(r.Pruned))
	patched := make([]string, 0, len(r.Patched))
	for loc := range r.Patched {
		patched = append(patched, loc)
	}
	sort.Strings(patched)
	for _, loc := range patched {
		rows = append(rows, []string{loc, "patched"})
	}
	for _, loc := range r.Pruned { // already sorted by the orchestrator
		rows = append(rows, []string{loc, "pruned (deleted)"})
	}
	return rows
}

func (p Payload) renderTable(w io.Writer) error {
	r := p.Result
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if r.DryRun {
		if _, err := fmt.Fprintln(tw, strings.Join(diffHeaders, "\t")); err != nil {
			return err
		}
		for _, row := range diffRows(r.Diff) {
			if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		return writeSummary(w, r.Diff.Summary)
	}
	rows := applyRows(r)
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no changes to apply (local tree already matches Play)")
		return err
	}
	if _, err := fmt.Fprintln(tw, strings.Join(applyHeaders, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// writeSummary prints the one-line tally under the dry-run table.
func writeSummary(w io.Writer, s diff.Summary) error {
	_, err := fmt.Fprintf(w,
		"summary: create=%d update=%d clear=%d unchanged=%d untouchedLocales=%d delete=%d\n",
		s.Create, s.Update, s.Clear, s.Unchanged, s.UntouchedLocales, s.Delete)
	return err
}

func (p Payload) renderMarkdown(w io.Writer) error {
	r := p.Result
	if r.DryRun {
		if err := output.MarkdownTable(w, diffHeaders, diffRows(r.Diff)); err != nil {
			return err
		}
		// Parity with renderTable: the dry-run human views both carry the
		// summary tally under the table.
		return writeSummary(w, r.Diff.Summary)
	}
	rows := applyRows(r)
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no changes to apply (local tree already matches Play)")
		return err
	}
	return output.MarkdownTable(w, applyHeaders, rows)
}

// renderJSON emits the gplay diff schema for a dry-run (ADR-0011 §6), or the
// per-locale listings.patch bodies for a real apply. A pruned locale is
// reported as {"pruned":true}.
func (p Payload) renderJSON(w io.Writer) error {
	r := p.Result
	if r.DryRun {
		return output.WriteJSON(w, r.Diff)
	}
	out := make(map[string]json.RawMessage, len(r.Patched)+len(r.Pruned))
	for loc, body := range r.Patched {
		out[loc] = body
	}
	for _, loc := range r.Pruned {
		out[loc] = json.RawMessage(`{"pruned":true}`)
	}
	return output.WriteJSON(w, out)
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}
	dir := in.Dir
	if dir == "" {
		dir = DefaultDir
	}

	local, err := tree.Read(dir)
	if err != nil {
		return nil, &dirError{dir: dir, cause: err}
	}

	// Both dry-run (online preview) and real apply need an authenticated
	// client — unlike `metadata validate`, apply always talks to Play.
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	res, err := orchestrator.Apply(rc.Ctx, httpClient, local, orchestrator.Opts{
		Package:     pkg,
		DryRun:      in.DryRun,
		Confirm:     in.Confirm,
		Prune:       in.Prune,
		AllowLocale: in.AllowLocale,
	})
	if err != nil {
		return nil, classifyEditError(pkg, err)
	}
	return Payload{Result: res}, nil
}

// NewCommand returns the cobra command for `gplay metadata apply`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile the local metadata tree with the Listings live on Play",
		Long: `Reconcile the local Metadata tree (--dir, default ./metadata) with the
Store front Listings live on Google Play for --package.

By default the sync is ADDITIVE: it upserts only the locales and fields
present on disk; a locale live on Play but absent locally is left intact
and reported. Use --prune to also delete online-only locales (it refuses
to remove the app's defaultLanguage). Note: a locale is "present on disk"
only if its directory holds at least one recognized field file
(title.txt, …) — a directory with only a README or unrecognized files is
NOT seen as managed and, under --prune, would be deleted online. Preview
with --dry-run first.

--dry-run reads the live Listings and prints the per-locale delta without
committing (it is ONLINE — it diffs disk against Play). --output json is
the gplay diff schema {package, changes[], summary}, so a CI gate is one
jq line: jq -e '.summary.create + .summary.update > 0'.

A real apply requires --confirm (every committed Listing is live on the
store immediately); without it apply refuses and points here. CI=true does
NOT auto-confirm. The publish is atomic: all locales patch inside one Edit
committed once, and any per-locale failure discards the Edit (0 published).`,
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
	cmd.Flags().StringVar(&in.Dir, "dir", DefaultDir, "metadata tree root directory")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "read live Play and print the delta without committing (online)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the real publish (Listings go live immediately)")
	cmd.Flags().BoolVar(&in.Prune, "prune", false, "also delete locales live on Play but absent on disk (refuses defaultLanguage)")
	cmd.Flags().StringArrayVar(&in.AllowLocale, "allow-locale", nil, "whitelist a locale code outside the embedded registry (repeatable)")
	return cmd
}
