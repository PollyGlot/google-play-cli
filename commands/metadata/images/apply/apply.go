// Package imagesapply implements `gplay metadata images apply`: reconcile the
// local Store-image tree with the images live on Google Play. It is thin glue
// : resolve --package/--dir, read the image tree off disk (internal/metadata/
// imagetree), validate the --type flags, and hand it to
// internal/metadata/imageorchestrator, which owns the Edit lifecycle, the diff,
// the --confirm gate, and the offline validate pre-check. The command only
// resolves inputs and renders.
//
// Two modes (ADR-0011 stance, ADR-0013 mechanics):
//
//   - --dry-run is ONLINE: it reads the live images and prints the per-slot
//     delta without committing. --output json is the ADR-0013 §5 diff schema
//     {package, slots[], summary} so a CI gate is one jq line:
//     jq -e '.summary.upload + .summary.delete + .summary.reorder > 0'.
//   - --confirm performs the real publish (one Edit, one commit, atomic).
//     Without --confirm a real apply refuses (exit 3: safety flag required)
//     and points at --dry-run; CI=true never auto-confirms. Images are live-on-commit (no draft).
package imagesapply

import (
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
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagediff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imageorchestrator"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// DefaultDir is the metadata tree root used when --dir is unset.
const DefaultDir = "./metadata"

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package    string
	Dir        string
	DryRun     bool
	Confirm    bool
	Prune      bool
	Locales    []string
	Types      []string
	NoValidate bool
}

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// dirError signals an unreadable --dir. Exit 20 (client-side validation).
type dirError struct {
	dir   string
	cause error
}

func (e *dirError) Error() string {
	return fmt.Sprintf("metadata dir %q is not readable: %v (create it, or run `gplay metadata images pull` to seed it)", e.dir, e.cause)
}
func (e *dirError) Unwrap() error { return e.cause }
func (e *dirError) ExitCode() int { return 20 }

type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found: run `gplay apps list` to see the packages registered with gplay: %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q: in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.pkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

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

// Payload renders an imageorchestrator.Result. The shape switches on
// Result.DryRun: a dry-run renders the diff schema; a real apply renders what
// was published.
type Payload struct {
	Result *imageorchestrator.Result
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Result.Diff) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

var diffHeaders = []string{"LOCALE", "IMAGE_TYPE", "OP", "SHA256", "POS"}

func diffRows(d imagediff.Result) [][]string {
	rows := make([][]string, 0, len(d.Slots))
	for _, c := range d.Slots {
		pos := ""
		if c.Position >= 0 {
			pos = strconv.Itoa(c.Position)
		}
		sha := c.Sha256
		if len(sha) > 12 {
			sha = sha[:12]
		}
		rows = append(rows, []string{c.Locale, c.ImageType, string(c.Op), sha, pos})
	}
	return rows
}

func (p Payload) renderTable(w io.Writer) error {
	r := p.Result
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
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
	verb := "planned"
	if !r.DryRun {
		verb = "applied"
	}
	_, err := fmt.Fprintf(w, "%s: upload=%d delete=%d reorder=%d unchanged=%d\n",
		verb, r.Diff.Summary.Upload, r.Diff.Summary.Delete, r.Diff.Summary.Reorder, r.Diff.Summary.Unchanged)
	return err
}

func (p Payload) renderMarkdown(w io.Writer) error {
	if err := output.MarkdownTable(w, diffHeaders, diffRows(p.Result.Diff)); err != nil {
		return err
	}
	s := p.Result.Diff.Summary
	_, err := fmt.Fprintf(w, "\nupload=%d delete=%d reorder=%d unchanged=%d\n", s.Upload, s.Delete, s.Reorder, s.Unchanged)
	return err
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
	}
	dir := in.Dir
	if dir == "" {
		dir = DefaultDir
	}

	// Refuse an unknown --type upfront (before any network) so a typo never
	// silently applies nothing.
	for _, t := range in.Types {
		if _, ok := images.ParseType(t); !ok {
			return nil, &usageError{msg: fmt.Sprintf("unknown --type %q (want one of: icon, featureGraphic, tvBanner, promoGraphic, phoneScreenshots, sevenInchScreenshots, tenInchScreenshots, tvScreenshots, wearScreenshots)", t)}
		}
	}

	local, err := imagetree.Read(dir)
	if err != nil {
		return nil, &dirError{dir: dir, cause: err}
	}

	// Refuse a --locale absent from the on-disk tree, mirroring the --type
	// guard: a typo'd locale (en_US vs en-US) is otherwise silently dropped
	// during reconciliation and the apply does nothing for it.
	for _, loc := range in.Locales {
		if _, ok := local[loc]; !ok {
			return nil, &usageError{msg: fmt.Sprintf("unknown --locale %q (no managed images under %s; on disk: %s)", loc, dir, strings.Join(managedLocales(local), ", "))}
		}
	}

	// UploadClient, not AuthedClient: image bytes (icons, feature graphics,
	// screenshots) are media uploads, exempt from the 60s control-plane default
	// (honors an explicit --timeout). See kernel.RunContext.UploadClient.
	httpClient, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}

	// Reuse an open explicit Edit when one is pinned (`gplay edits begin`); ""
	// keeps the implicit per-apply Edit. Skipped on --dry-run: the preview uses
	// a separate read-only Edit and never reuses the pin, so a corrupt pin must
	// not fail it.
	var explicitEditID string
	if !in.DryRun {
		if explicitEditID, err = rc.ExplicitEditID(pkg); err != nil {
			return nil, err
		}
	}

	res, err := imageorchestrator.Apply(rc.Ctx, httpClient, local, imageorchestrator.Opts{
		Package:        pkg,
		DryRun:         in.DryRun,
		Confirm:        in.Confirm,
		Prune:          in.Prune,
		Locales:        in.Locales,
		Types:          in.Types,
		NoValidate:     in.NoValidate,
		ExplicitEditID: explicitEditID,
	})
	if err != nil {
		return nil, classifyEditError(pkg, err)
	}
	// DESIGN §8: a committed apply prints one ✓ line on stderr (never on a
	// --dry-run), reporting the upload/delete/reorder tally.
	if !in.DryRun {
		s := res.Diff.Summary
		rc.ConfirmMutation(explicitEditID, "images applied to %q (%d uploaded, %d deleted, %d reordered)",
			res.Package, s.Upload, s.Delete, s.Reorder)
	}
	return Payload{Result: res}, nil
}

// managedLocales returns the locale codes the on-disk tree manages (has images
// for), sorted: used to name the available locales when a --locale is unknown.
func managedLocales(local imagetree.Tree) []string {
	out := make([]string, 0, len(local))
	for loc := range local {
		out = append(out, loc)
	}
	sort.Strings(out)
	return out
}

// NewCommand returns the cobra command for `gplay metadata images apply`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile the local Store-image tree with the images live on Play",
		Long: `Reconcile the local Store-image tree (--dir, default ./metadata) with the
images live on Google Play for --package.

The sync is ADDITIVE: it uploads images present on disk and reorders a
gallery whose order changed, but an image live on Play yet absent from a
managed slot is left intact (removing online-only images is ` +
			"`--prune`" + `, a separate destructive mode). A slot absent or empty
on disk is unmanaged and never touched.

--dry-run reads the live images and prints the per-slot delta without
committing (it is ONLINE: it diffs disk against Play). --output json is
the diff schema {package, slots[], summary}, so a CI gate is one jq line:
jq -e '.summary.upload + .summary.delete + .summary.reorder > 0'.

A real apply requires --confirm (every committed image is live on the
store immediately; there is no draft). Without it apply refuses and points
here. CI=true does NOT auto-confirm. The publish is atomic: all slots
reconcile inside one Edit committed once, and any per-slot failure discards
the Edit (0 published).

` + "`metadata images validate`" + ` runs as a fail-fast pre-check;
--no-validate bypasses it (Play's commit stays the ultimate authority).
--locale and --type restrict the reconciliation to a subset of slots.`,
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
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the real publish (images go live immediately)")
	cmd.Flags().BoolVar(&in.Prune, "prune", false, "also delete a managed slot's online-only images (destructive; requires --confirm)")
	cmd.Flags().StringArrayVar(&in.Locales, "locale", nil, "restrict to these locale codes (repeatable)")
	cmd.Flags().StringArrayVar(&in.Types, "type", nil, "restrict to these image types (repeatable)")
	cmd.Flags().BoolVar(&in.NoValidate, "no-validate", false, "skip the offline image validation pre-check (Play's commit stays the authority)")
	return cmd
}
