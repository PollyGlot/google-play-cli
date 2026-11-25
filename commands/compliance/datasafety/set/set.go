package set

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/compliance/datasafety"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	playds "github.com/PollyGlot/google-play-cli/internal/play/datasafety"
)

const DefaultFile = "./compliance/data-safety.csv"

type Input struct {
	Package string
	File    string
	DryRun  bool
	Confirm bool
}

func confirmRequired() error {
	return exit.SafetyFlag("confirm", "compliance datasafety set replaces the app's live Data Safety declaration (a stale or wrong declaration can block releases); pass --confirm to proceed (preview first with --dry-run)")
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
	Package  string
	Account  string
	Bytes    int
	Rows     int
	DryRun   bool
	Warnings []string
	Raw      json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func dryRunSuffix(dryRun bool) string {
	if dryRun {
		return " (dry-run)"
	}
	return ""
}

func (p Payload) renderTable(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "PACKAGE: %s%s\n", p.Package, dryRunSuffix(p.DryRun)); err != nil {
		return err
	}
	verb := "POSTed"
	if p.DryRun {
		verb = "would POST"
	}
	if _, err := fmt.Fprintf(w, "%s %d bytes / %d rows\n", verb, p.Bytes, p.Rows); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if p.Account != "" {
		if _, err := fmt.Fprintf(tw, "account\t%s\n", p.Account); err != nil {
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
	return nil
}

type jsonView struct {
	DryRun   bool     `json:"dryRun"`
	Package  string   `json:"package"`
	Account  string   `json:"account,omitempty"`
	Bytes    int      `json:"bytes"`
	Rows     int      `json:"rows"`
	Warnings []string `json:"warnings,omitempty"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, jsonView{
			DryRun:   p.DryRun,
			Package:  p.Package,
			Account:  p.Account,
			Bytes:    p.Bytes,
			Rows:     p.Rows,
			Warnings: p.Warnings,
		})
	}
	return output.WriteAckOrRaw(w, p.Raw, struct {
		OK      bool   `json:"ok"`
		Package string `json:"package"`
		Bytes   int    `json:"bytes"`
		Rows    int    `json:"rows"`
	}{OK: true, Package: p.Package, Bytes: p.Bytes, Rows: p.Rows})
}

func (p Payload) renderMarkdown(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "## %s%s\n\n", p.Package, dryRunSuffix(p.DryRun)); err != nil {
		return err
	}
	verb := "POSTed"
	if p.DryRun {
		verb = "Would POST"
	}
	rows := [][]string{
		{"Action", fmt.Sprintf("%s %d bytes / %d rows", verb, p.Bytes, p.Rows)},
	}
	if p.Account != "" {
		rows = append(rows, []string{"Account", p.Account})
	}
	if err := output.MarkdownTable(w, []string{"FIELD", "VALUE"}, rows); err != nil {
		return err
	}
	for _, warn := range p.Warnings {
		if _, err := fmt.Fprintf(w, "\n> **hint:** %s\n", warn); err != nil {
			return err
		}
	}
	return nil
}

func loadAndValidate(file string) (*datasafety.Report, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, &fileError{file: file, cause: err}
	}
	rep, problems := datasafety.Validate(raw)
	if len(problems) > 0 {
		return nil, &ValidationError{File: file, Problems: problems}
	}
	return rep, nil
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	file := in.File
	if file == "" {
		file = DefaultFile
	}

	rep, err := loadAndValidate(file)
	if err != nil {
		return nil, err
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	if in.DryRun {
		return Payload{
			Package:  pkg,
			Account:  rc.AccountName,
			Bytes:    len(rep.Payload),
			Rows:     rep.Rows,
			DryRun:   true,
			Warnings: rep.Warnings,
		}, nil
	}

	if !in.Confirm {
		return nil, confirmRequired()
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	raw, err := playds.Post(rc.Ctx, httpClient, pkg, rep.Payload)
	if err != nil {
		return nil, apihint.ForPackage(pkg, err)
	}

	rc.Confirmf("data safety submitted for %q (%d rows)", pkg, rep.Rows)
	return Payload{
		Package:  pkg,
		Account:  rc.AccountName,
		Bytes:    len(rep.Payload),
		Rows:     rep.Rows,
		Warnings: rep.Warnings,
		Raw:      raw,
	}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Push the app's Data Safety declaration from the canonical CSV",
		Long: `Push the canonical Data Safety CSV (--file, default
./compliance/data-safety.csv) to Google as the app's Data Safety declaration.
The declaration is write-only and replaces the whole document: a direct POST
outside the Edits model. gplay cannot read it back; only this POST
validates the contents.

set runs ` + "`validate`" + ` implicitly first, so a structurally invalid CSV
never reaches the network. Use --dry-run to rehearse the write: it validates
the CSV, resolves the target package and Account, and reports "would POST N
bytes / N rows to <package>" without any network call (and without needing
--confirm).

The real write requires --confirm (a stale or wrong declaration can block
releases or misstate your data practices); without it set refuses, exits 3
(safety flag required), and points here. CI=true does NOT auto-confirm.
--output json passes the API response through verbatim (or, when the API
returns an empty body, a gplay-shaped success object).`,
		Example: `  # Rehearse: validate the CSV and resolve the target, no HTTP call
  gplay compliance datasafety set --dry-run

  # Replace the live Data Safety declaration
  gplay compliance datasafety set --file compliance/data-safety.csv --confirm`,
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
	cmd.Flags().StringVar(&in.File, "file", DefaultFile, "path to the canonical Data Safety CSV to push")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "rehearse the write (validate + resolve target + report size) without any HTTP call")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the real write (replaces the live Data Safety declaration)")
	return cmd
}
