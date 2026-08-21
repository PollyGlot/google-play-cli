// Package validatecmd implements `gplay metadata validate`: an OFFLINE
// lint of the on-disk Metadata tree. Unlike every other gplay command
// that touches Play, this one never authenticates and never makes an HTTP
// call: it reads the tree off disk via internal/metadata/tree and runs
// the pure internal/metadata/validate engine, so it is safe to wire into
// a pre-commit hook or a CI gate with no credentials present.
//
// This is the offline half of the metadata sync model (ADR-0011 §3): the
// online `metadata apply --dry-run` diffs disk against Play, while
// `metadata validate` checks the rules that need no network: char
// limits, known locales, required-non-empty fields. A non-empty result is
// exit 20 (client-side validation, docs/DESIGN.md §9).
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

// DefaultDir is the conventional metadata-tree root, matching the rest of
// the metadata command family and `fastlane supply`.
const DefaultDir = "./metadata"

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Dir         string
	AllowLocale []string
}

// dirError signals the metadata tree could not be read (missing or
// unreadable directory). It is a client-side validation failure: the
// operator pointed --dir at something gplay cannot lint, so ExitCode()
// is 20 per docs/DESIGN.md §9.
type dirError struct {
	dir   string
	cause error
}

func (e *dirError) Error() string {
	return fmt.Sprintf("cannot read metadata tree at %s: %v: pass --dir <path> to point at your metadata directory", e.dir, e.cause)
}

func (e *dirError) ExitCode() int { return 20 }

func (e *dirError) Unwrap() error { return e.cause }

// ValidationError carries every lint Problem found in the tree. It is the
// command's failure mode: ExitCode() is 20 (client-side validation,
// docs/DESIGN.md §9) and Error() lists every problem, one actionable line
// each, so a CI log shows the full picture in one run rather than failing
// on the first violation.
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

// Payload is the success view: the tree passed, here is what was checked.
// It carries the validated locale list so the table/markdown/json forms
// can show the operator exactly which locales were linted.
type Payload struct {
	Dir     string   `json:"dir"`
	Locales []string `json:"locales"`
	OK      bool     `json:"ok"`
}

// Renderers satisfies output.Renderable. JSON is a small gplay-defined
// success object (this command has no API pass-through to mirror); table
// and markdown are human summaries.
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

// allowSet turns the repeated --allow-locale values into a lookup set.
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

// Run is the business function the kernel invokes. It is OFFLINE: it reads
// the tree off disk and runs the pure validator. It NEVER calls
// rc.AuthedClient and makes no HTTP request, so it works with no
// credentials and no network (pre-commit / CI).
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

// NewCommand returns the cobra command for `gplay metadata validate`.
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
