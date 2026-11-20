package imagesapply

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagediff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imageorchestrator"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

const DefaultDir = "./metadata"

type Input struct {
	Package       string
	Dir           string
	DryRun        bool
	Confirm       bool
	Prune         bool
	Commit        commitflags.Flags
	Locales       []string
	Types         []string
	SkipPreflight bool
	AIGenerated   *bool
}

type dirError struct {
	dir   string
	cause error
}

func (e *dirError) Error() string {
	return fmt.Sprintf("metadata dir %q is not readable: %v (create it, or run `gplay metadata images pull` to seed it)", e.dir, e.cause)
}
func (e *dirError) Unwrap() error { return e.cause }
func (e *dirError) ExitCode() int { return 20 }

type listingNotFoundError struct {
	locale string
	dir    string
	cause  error
}

func (e *listingNotFoundError) Error() string {
	return fmt.Sprintf("no Listing for language %q on Play (the package itself was found): images attach to a Listing, so create the %q Listing first with `gplay metadata apply` (it reads title.txt and full_description.txt from %s), then rerun: %v",
		e.locale, e.locale, filepath.Join(e.dir, e.locale), e.cause)
}
func (e *listingNotFoundError) Unwrap() error { return e.cause }

func classifyApplyError(pkg, dir string, err error) error {
	var locErr *imageorchestrator.LocaleError
	if apihint.Status(err) == http.StatusNotFound && errors.As(err, &locErr) {
		return &listingNotFoundError{locale: locErr.Locale, dir: dir, cause: err}
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return &dirError{dir: dir, cause: err}
	}
	return apihint.ForPackage(pkg, err)
}

type Payload struct {
	Result *imageorchestrator.Result
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) renderJSON(w io.Writer) error {
	return output.WriteJSON(w, struct {
		DryRun bool `json:"dryRun,omitempty"`
		imagediff.Result
	}{DryRun: p.Result.DryRun, Result: p.Result.Diff})
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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	dir := in.Dir
	if dir == "" {
		dir = DefaultDir
	}

	for _, t := range in.Types {
		if _, ok := images.ParseType(t); !ok {
			return nil, exit.Usagef("unknown --type %q (want one of: %s)", t, images.Names())
		}
	}

	local, err := imagetree.Scan(dir)
	if err != nil {
		return nil, &dirError{dir: dir, cause: err}
	}
	retired, err := imagetree.Retired(dir)
	if err != nil {
		return nil, &dirError{dir: dir, cause: err}
	}
	for _, p := range retired {
		rc.Warnf("%s", imagetree.RetiredMessage(p))
	}

	for _, loc := range in.Locales {
		if _, ok := local[loc]; !ok {
			return nil, exit.Usagef("unknown --locale %q (no managed images under %s; on disk: %s)", loc, dir, strings.Join(managedLocales(local), ", "))
		}
	}

	httpClient, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}

	var explicitEditID string
	if !in.DryRun {
		if explicitEditID, err = rc.ExplicitEditID(pkg); err != nil {
			return nil, err
		}
	}

	res, err := imageorchestrator.Apply(rc.Ctx, httpClient, local, imageorchestrator.Opts{
		Package:        pkg,
		Dir:            dir,
		DryRun:         in.DryRun,
		Confirm:        in.Confirm,
		Prune:          in.Prune,
		Locales:        in.Locales,
		Types:          in.Types,
		NoValidate:     in.SkipPreflight,
		ExplicitEditID: explicitEditID,
		Commit:         in.Commit.For(rc, explicitEditID),
		AIGenerated:    aiGeneratedState(in.AIGenerated),
	})
	if err != nil {
		return nil, classifyApplyError(pkg, dir, err)
	}
	if !in.DryRun {
		s := res.Diff.Summary
		rc.ConfirmMutation(explicitEditID, "images applied to %q (%d uploaded, %d deleted, %d reordered)",
			res.Package, s.Upload, s.Delete, s.Reorder)
	}
	return Payload{Result: res}, nil
}

func aiGeneratedState(declared *bool) images.AIGeneratedState {
	switch {
	case declared == nil:
		return ""
	case *declared:
		return images.AIGenerated
	default:
		return images.NotAIGenerated
	}
}

func managedLocales(local imagetree.Staged) []string {
	out := make([]string, 0, len(local))
	for loc := range local {
		out = append(out, loc)
	}
	sort.Strings(out)
	return out
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag  string
		in          Input
		aiGenerated bool
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
--skip-preflight bypasses it (Play's commit stays the ultimate authority).
--locale and --type restrict the reconciliation to a subset of slots.

--ai-generated attests that the images this apply uploads were generated by
AI; --ai-generated=false attests they were not. Play does not require the
attestation, and without the flag none is sent. It rides each upload only:
an image already live with the same content is not uploaded again, so its
attestation stays as it was. Combine with --locale and --type to attest a
subset.`,
		Example: `  # Show what would change on Play, per slot (online, nothing committed)
  gplay metadata images apply --dry-run

  # Publish only the en-US phone screenshots
  gplay metadata images apply --locale en-US --type phoneScreenshots --confirm

  # Also delete managed slots' images that exist only on Play
  gplay metadata images apply --prune --confirm`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed(aiGeneratedFlag) {
				in.AIGenerated = &aiGenerated
			}
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
	commitflags.Register(cmd, &in.Commit)
	cmd.Flags().BoolVar(&in.Prune, "prune", false, "also delete a managed slot's online-only images (destructive; requires --confirm)")
	cmd.Flags().StringArrayVar(&in.Locales, "locale", nil, "restrict to these locale codes (repeatable)")
	cmd.Flags().StringArrayVar(&in.Types, "type", nil, "restrict to these image types (repeatable)")
	cmd.Flags().BoolVar(&in.SkipPreflight, "skip-preflight", false, "skip the offline image validation pre-check (Play's commit stays the authority)")
	cmd.Flags().BoolVar(&aiGenerated, aiGeneratedFlag, false, "attest that the uploaded images are AI generated (=false: not AI generated); unset sends no attestation")
	return kernel.ExperimentalFlag(cmd, aiGeneratedFlag)
}

const aiGeneratedFlag = "ai-generated"
