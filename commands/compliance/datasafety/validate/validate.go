package validatecmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/compliance/datasafety"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const DefaultFile = "./compliance/data-safety.csv"

type Input struct {
	File string
}

type fileError struct {
	file  string
	cause error
}

func (e *fileError) Error() string {
	return fmt.Sprintf("cannot read Data Safety CSV at %s: %v: pass --file <path> to point at your declaration", e.file, e.cause)
}
func (e *fileError) ExitCode() int { return 20 }
func (e *fileError) Unwrap() error { return e.cause }

type ValidationError struct {
	File     string
	Problems []datasafety.Problem
}

func (e *ValidationError) ExitCode() int { return 20 }

func (e *ValidationError) Error() string {
	n := len(e.Problems)
	var b strings.Builder
	fmt.Fprintf(&b, "Data Safety CSV at %s failed structural validation: %d problem", e.File, n)
	if n != 1 {
		b.WriteByte('s')
	}
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p.Message)
	}
	return b.String()
}

type Payload struct {
	File     string   `json:"file"`
	OK       bool     `json:"ok"`
	Rows     int      `json:"rows"`
	Columns  int      `json:"columns"`
	Warnings []string `json:"warnings,omitempty"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderText(w) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) renderText(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	rows := [][2]string{
		{"FILE", p.File},
		{"STATUS", "structurally valid"},
		{"ROWS", strconv.Itoa(p.Rows)},
		{"COLUMNS", strconv.Itoa(p.Columns)},
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", r[0], r[1]); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, warn := range p.Warnings {
		if _, err := fmt.Fprintf(w, "hint: %s\n", warn); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "note: structural check only: only the live `set` POST tells you whether Google accepts the declaration.")
	return err
}

func (p Payload) renderMarkdown(w io.Writer) error {
	rows := [][]string{
		{"File", p.File},
		{"Status", "structurally valid"},
		{"Rows", fmt.Sprintf("%d", p.Rows)},
		{"Columns", fmt.Sprintf("%d", p.Columns)},
	}
	if err := output.MarkdownTable(w, []string{"FIELD", "VALUE"}, rows); err != nil {
		return err
	}
	for _, warn := range p.Warnings {
		if _, err := fmt.Fprintf(w, "\n> **hint:** %s\n", warn); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "\n_Structural check only: only the live `set` POST tells you whether Google accepts the declaration._")
	return err
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	file := in.File
	if file == "" {
		file = DefaultFile
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, &fileError{file: file, cause: err}
	}

	rep, problems := datasafety.Validate(raw)
	if len(problems) > 0 {
		return nil, &ValidationError{File: file, Problems: problems}
	}

	return Payload{
		File:     file,
		OK:       true,
		Rows:     rep.Rows,
		Columns:  rep.Columns,
		Warnings: rep.Warnings,
	}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Structurally check the Data Safety CSV offline (no network, no auth)",
		Long: `Structurally check the canonical Data Safety CSV (--file, default
./compliance/data-safety.csv) without contacting Google Play: it verifies
the file is non-empty, valid UTF-8 (a leading byte-order mark is stripped),
parses as a well-formed, rectangular CSV, and has a header row. A header
that diverges from gplay's bundled reference template is reported as a
NON-FATAL hint (Google's template evolves), never a failure.

This command is OFFLINE: it needs no credentials and makes no network
call, so it is safe in a pre-commit hook or a CI gate. It is also STRUCTURAL
ONLY: gplay cannot read your live Data Safety declaration back (the API is
write-only), so a CSV that passes here can still be rejected by Google.
"Valid" means "well-formed", NOT "Google will accept it": only the live
` + "`gplay compliance datasafety set`" + ` POST arbitrates that.

Any structural failure exits 20.`,
		Example: `  # Lint ./compliance/data-safety.csv offline, e.g. in a pre-commit hook
  gplay compliance datasafety validate

  gplay compliance datasafety validate --file store/data-safety.csv --output json`,
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
	cmd.Flags().StringVar(&in.File, "file", DefaultFile, "path to the canonical Data Safety CSV to validate")
	return cmd
}
