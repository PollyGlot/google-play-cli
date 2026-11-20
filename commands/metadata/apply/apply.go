package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/diff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const DefaultDir = "./metadata"

type Input struct {
	Package     string
	Dir         string
	DryRun      bool
	Confirm     bool
	Prune       bool
	Commit      commitflags.Flags
	AllowLocale []string
}

type dirError struct {
	dir   string
	cause error
}

func (e *dirError) Error() string {
	return fmt.Sprintf("metadata dir %q is not readable: %v (create it, or run `gplay metadata pull` to seed it)", e.dir, e.cause)
}
func (e *dirError) Unwrap() error { return e.cause }
func (e *dirError) ExitCode() int { return 20 }

type listingNotFoundError struct {
	locale string
	cause  error
}

func (e *listingNotFoundError) Error() string {
	return fmt.Sprintf("no Listing for language %q on Play (the package itself was found): check the locale code, or preview with `gplay metadata apply --dry-run`: %v", e.locale, e.cause)
}
func (e *listingNotFoundError) Unwrap() error { return e.cause }

func classifyApplyError(pkg string, err error) error {
	var locErr *orchestrator.LocaleError
	if apihint.Status(err) == http.StatusNotFound && errors.As(err, &locErr) {
		return &listingNotFoundError{locale: locErr.Locale, cause: err}
	}
	return apihint.ForPackage(pkg, err)
}

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

var applyHeaders = []string{"LOCALE", "RESULT"}

func applyRows(r *orchestrator.Result) [][]string {
	rows := make([][]string, 0, len(r.Patched)+len(r.Pruned))
	patched := make([]string, 0, len(r.Patched))
	for loc := range r.Patched {
		patched = append(patched, loc)
	}
	sort.Strings(patched)
	created := make(map[string]bool, len(r.Created))
	for _, loc := range r.Created {
		created[loc] = true
	}
	for _, loc := range patched {
		action := "patched"
		if created[loc] {
			action = "created"
		}
		rows = append(rows, []string{loc, action})
	}
	for _, loc := range r.Pruned {
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
		return writeSummary(w, r.Diff.Summary)
	}
	rows := applyRows(r)
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no changes to apply (local tree already matches Play)")
		return err
	}
	return output.MarkdownTable(w, applyHeaders, rows)
}

func (p Payload) renderJSON(w io.Writer) error {
	r := p.Result
	if r.DryRun {
		return output.WriteJSON(w, struct {
			DryRun bool `json:"dryRun"`
			diff.Result
		}{DryRun: true, Result: r.Diff})
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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	dir := in.Dir
	if dir == "" {
		dir = DefaultDir
	}

	local, err := tree.Read(dir)
	if err != nil {
		return nil, &dirError{dir: dir, cause: err}
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	var explicitEditID string
	if !in.DryRun {
		if explicitEditID, err = rc.ExplicitEditID(pkg); err != nil {
			return nil, err
		}
	}

	res, err := orchestrator.Apply(rc.Ctx, httpClient, local, orchestrator.Opts{
		Package:        pkg,
		DryRun:         in.DryRun,
		Confirm:        in.Confirm,
		Prune:          in.Prune,
		AllowLocale:    in.AllowLocale,
		ExplicitEditID: explicitEditID,
		Commit:         in.Commit.For(rc, explicitEditID),
	})
	if err != nil {
		return nil, classifyApplyError(pkg, err)
	}
	verb := "creates"
	if in.DryRun {
		verb = "would create"
	}
	for _, inc := range res.Incomplete {
		rc.Warnf("this apply %s locale %q on Play without %s, which `gplay metadata validate` requires: add %s under %s",
			verb, inc.Locale, strings.Join(inc.Missing, " and "), missingFiles(inc.Missing), filepath.Join(dir, inc.Locale))
	}
	if !in.DryRun {
		detail := fmt.Sprintf("%d locale(s) patched", len(res.Patched))
		if len(res.Created) > 0 {
			detail = fmt.Sprintf("%d locale(s) created, %d patched", len(res.Created), len(res.Patched)-len(res.Created))
		}
		if len(res.Pruned) > 0 {
			detail += fmt.Sprintf(", %d pruned", len(res.Pruned))
		}
		rc.ConfirmMutation(explicitEditID, "metadata applied to %q (%s)", res.Package, detail)
	}
	return Payload{Result: res}, nil
}

func missingFiles(keys []string) string {
	files := make([]string, 0, len(keys))
	for _, k := range keys {
		if spec, ok := listing.SpecByKey(k); ok {
			files = append(files, spec.File)
		}
	}
	return strings.Join(files, " and ")
}

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
(title.txt, …): a directory with only a README or unrecognized files is
NOT seen as managed and, under --prune, would be deleted online. Preview
with --dry-run first.

--dry-run reads the live Listings and prints the per-locale delta without
committing (it is ONLINE: it diffs disk against Play). --output json is
the gplay diff schema {package, changes[], summary}, so a CI gate is one
jq line: jq -e '.summary.create + .summary.update > 0'.

A real apply requires --confirm (every committed Listing is live on the
store immediately); without it apply refuses and points here. CI=true does
NOT auto-confirm. The publish is atomic: all locales are written inside one
Edit committed once (a locale new to Play is created, a live one patched),
and any per-locale failure discards the Edit (0 published).`,
		Example: `  # Show what would change on Play, per locale (online, nothing committed)
  gplay metadata apply --dry-run

  # Publish every locale on disk in one Edit
  gplay metadata apply --confirm

  # Also delete the locales that exist only on Play
  gplay metadata apply --prune --confirm`,
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
	commitflags.Register(cmd, &in.Commit)
	cmd.Flags().BoolVar(&in.Prune, "prune", false, "also delete locales live on Play but absent on disk (refuses defaultLanguage)")
	cmd.Flags().StringArrayVar(&in.AllowLocale, "allow-locale", nil, "whitelist a locale code outside the embedded registry (repeatable)")
	return cmd
}
