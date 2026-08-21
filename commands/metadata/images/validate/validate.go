// Package imagesvalidate implements `gplay metadata images validate`: an
// OFFLINE lint of the on-disk Store-image tree. Like `metadata validate`
// (text), it never authenticates and never makes an HTTP call: it reads the
// image tree off disk via internal/metadata/imagetree and runs the pure
// internal/metadata/imagevalidate engine, so it is safe in a pre-commit hook
// or a CI gate with no credentials present.
//
// This is the offline half of the image sync model (ADR-0013 §4): the online
// `metadata images apply --dry-run` diffs disk against Play, while this
// command checks the rules that need no network: exact dimensions, screenshot
// side range + aspect ratio, format (png/jpeg), per-image byte size, and
// per-slot count. A non-empty result is exit 20 (client-side validation). It
// is the DEFAULT gate but never the ultimate authority: `images apply` runs it
// as a fail-fast pre-check that `--no-validate` bypasses, so a stale rule table
// can never permanently block an upload Play would accept.
package imagesvalidate

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagevalidate"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// DefaultDir matches the rest of the metadata family and `fastlane supply`.
const DefaultDir = "./metadata"

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Dir string
}

// dirError signals the image tree could not be read. It is a client-side
// validation failure (exit 20).
type dirError struct {
	dir   string
	cause error
}

func (e *dirError) Error() string {
	return fmt.Sprintf("cannot read metadata tree at %s: %v: pass --dir <path> to point at your metadata directory", e.dir, e.cause)
}
func (e *dirError) ExitCode() int { return 20 }
func (e *dirError) Unwrap() error { return e.cause }

// ValidationError carries every image rule violation found in the tree. It is
// the command's failure mode: ExitCode() 20, and Error() lists every problem
// one actionable line each, so a CI log shows the full picture in one run.
type ValidationError struct {
	Problems []imagevalidate.Problem
}

func (e *ValidationError) ExitCode() int { return 20 }

func (e *ValidationError) Error() string {
	n := len(e.Problems)
	var b strings.Builder
	fmt.Fprintf(&b, "image validation failed: %d problem", n)
	if n != 1 {
		b.WriteByte('s')
	}
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		if p.Index >= 0 {
			fmt.Fprintf(&b, "[%s/%s #%d] ", p.Locale, p.ImageType, p.Index+1)
		} else {
			fmt.Fprintf(&b, "[%s/%s] ", p.Locale, p.ImageType)
		}
		fmt.Fprintf(&b, "(%s) %s", p.Rule, p.Message)
	}
	return b.String()
}

// Payload is the success view: the tree passed, here is what was checked. It
// carries the validated slot count and the rule-table version so the operator
// can see exactly which limits were applied.
type Payload struct {
	Dir          string `json:"dir"`
	Slots        int    `json:"slots"`
	Images       int    `json:"images"`
	RulesVersion string `json:"rulesVersion"`
	OK           bool   `json:"ok"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderText(w) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) renderText(w io.Writer) error {
	_, err := fmt.Fprintf(w, "OK: %d image(s) across %d slot(s) valid in %s (rules %s)\n",
		p.Images, p.Slots, p.Dir, p.RulesVersion)
	return err
}

func (p Payload) renderMarkdown(w io.Writer) error {
	return output.MarkdownTable(w,
		[]string{"DIR", "SLOTS", "IMAGES", "RULES", "STATUS"},
		[][]string{{p.Dir, strconv.Itoa(p.Slots), strconv.Itoa(p.Images), p.RulesVersion, "valid"}})
}

// Run is OFFLINE: it reads the tree off disk and runs the pure validator. It
// NEVER calls rc.AuthedClient and makes no HTTP request.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	dir := in.Dir
	if dir == "" {
		dir = DefaultDir
	}

	tr, err := imagetree.Read(dir)
	if err != nil {
		return nil, &dirError{dir: dir, cause: err}
	}

	if problems := imagevalidate.Validate(tr); len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}

	slots, imgs := count(tr)
	return Payload{Dir: dir, Slots: slots, Images: imgs, RulesVersion: imagevalidate.RulesVersion, OK: true}, nil
}

// count returns the number of managed slots and total images in tr.
func count(tr imagetree.Tree) (slots, imgs int) {
	locales := make([]string, 0, len(tr))
	for loc := range tr {
		locales = append(locales, loc)
	}
	sort.Strings(locales)
	for _, loc := range locales {
		for _, ty := range images.Types() {
			seq := tr[loc][ty]
			if len(seq) == 0 {
				continue
			}
			slots++
			imgs += len(seq)
		}
	}
	return slots, imgs
}

// NewCommand returns the cobra command for `gplay metadata images validate`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Lint the on-disk Store-image tree offline (no network, no auth)",
		Long: `Lint the Store images under --dir without contacting Google Play: checks
exact dimensions (icon 512×512, feature graphic 1024×500, TV banner
1280×720), screenshot side range (320–3840 px) and 2:1 aspect ratio,
format (PNG/JPEG only, read from the bytes), per-image byte size, and
per-slot count (≤8).

This command is OFFLINE (no credentials, no network) so it is safe in a
pre-commit hook or a CI gate. Diffing the tree against what is live on Play
is the job of ` + "`gplay metadata images apply --dry-run`" + `.

The rules are a versioned in-code table (Play's commit is the ultimate
authority); ` + "`images apply --no-validate`" + ` bypasses this check.
Any violation exits 20.`,
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
	cmd.Flags().StringVar(&in.Dir, "dir", DefaultDir, "path to the metadata tree to lint")
	return cmd
}
