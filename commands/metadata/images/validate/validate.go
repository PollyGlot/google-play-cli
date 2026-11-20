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

const DefaultDir = "./metadata"

type Input struct {
	Dir string
}

type dirError struct {
	dir   string
	cause error
}

func (e *dirError) Error() string {
	return fmt.Sprintf("cannot read metadata tree at %s: %v: pass --dir <path> to point at your metadata directory", e.dir, e.cause)
}
func (e *dirError) ExitCode() int { return 20 }
func (e *dirError) Unwrap() error { return e.cause }

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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	dir := in.Dir
	if dir == "" {
		dir = DefaultDir
	}

	tr, err := imagetree.Scan(dir)
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

	problems, err := imagevalidate.Validate(tr)
	if err != nil {
		return nil, &dirError{dir: dir, cause: err}
	}
	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}

	slots, imgs := count(tr)
	return Payload{Dir: dir, Slots: slots, Images: imgs, RulesVersion: imagevalidate.RulesVersion, OK: true}, nil
}

func count(tr imagetree.Staged) (slots, imgs int) {
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
authority); ` + "`images apply --skip-preflight`" + ` bypasses this check.
Any violation exits 20.`,
		Example: `  # Check dimensions, format and counts offline, e.g. in CI
  gplay metadata images validate

  gplay metadata images validate --dir store/metadata --output json`,
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
