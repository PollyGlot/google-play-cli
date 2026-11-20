package validatecmd

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/metadata/validate"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const DefaultDir = "./metadata"

type Input struct {
	Dir         string
	AllowLocale []string
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
	Problems []validate.Problem
}

func (e *ValidationError) ExitCode() int { return 20 }

func (e *ValidationError) Error() string {
	n := len(e.Problems)
	var b strings.Builder
	fmt.Fprintf(&b, "metadata validation failed: %d problem", n)
	if n != 1 {
		b.WriteByte('s')
	}
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		if p.Field != "" {
			fmt.Fprintf(&b, "[%s/%s] ", p.Locale, p.Field)
		} else {
			fmt.Fprintf(&b, "[%s] ", p.Locale)
		}
		b.WriteString(p.Message)
	}
	return b.String()
}

type Payload struct {
	Dir     string   `json:"dir"`
	Locales []string `json:"locales"`
	OK      bool     `json:"ok"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderText(w) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) renderText(w io.Writer) error {
	_, err := fmt.Fprintf(w, "OK: %d locale(s) valid in %s: %s\n",
		len(p.Locales), p.Dir, strings.Join(p.Locales, ", "))
	return err
}

func (p Payload) renderMarkdown(w io.Writer) error {
	rows := make([][]string, len(p.Locales))
	for i, loc := range p.Locales {
		rows[i] = []string{loc, "valid"}
	}
	return output.MarkdownTable(w, []string{"LOCALE", "STATUS"}, rows)
}

func allowSet(codes []string) map[string]bool {
	if len(codes) == 0 {
		return nil
	}
	m := make(map[string]bool, len(codes))
	for _, c := range codes {
		if c = strings.TrimSpace(c); c != "" {
			m[c] = true
		}
	}
	return m
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	dir := in.Dir
	if dir == "" {
		dir = DefaultDir
	}

	tr, err := tree.Read(dir)
	if err != nil {
		return nil, &dirError{dir: dir, cause: err}
	}

	problems := validate.Validate(tr, allowSet(in.AllowLocale))
	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}

	locales := tr.Locales()
	sort.Strings(locales)
	return Payload{Dir: dir, Locales: locales, OK: true}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Lint the on-disk metadata tree offline (no network, no auth)",
		Long: `Lint the Metadata tree under --dir without contacting Google Play:
checks character limits (title 30, short description 80, full description
4000), required non-empty fields (title and full description: an empty
file is a validation error, not a clear), and that every locale directory
names a known Play store locale.

This command is OFFLINE: it needs no credentials and makes no network
call, so it is safe in a pre-commit hook or a CI gate. Diffing the tree
against what is live on Play is the job of ` +
			"`gplay metadata apply --dry-run`" + `.

A locale Google added after this gplay release can be whitelisted with
--allow-locale xx-YY (repeatable). Any violation exits 20.`,
		Example: `  # Lint ./metadata offline, e.g. in a pre-commit hook
  gplay metadata validate

  # Lint another tree and accept a locale newer than this gplay release
  gplay metadata validate --dir store/metadata --allow-locale ks-IN`,
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
	cmd.Flags().StringArrayVar(&in.AllowLocale, "allow-locale", nil,
		"whitelist a locale code not in gplay's embedded Play-locale list (repeatable)")
	return cmd
}
