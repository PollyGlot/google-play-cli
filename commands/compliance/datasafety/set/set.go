// Package set implements `gplay compliance datasafety set`: it pushes the
// app's Data Safety declaration to Google from the canonical CSV. The
// declaration is WRITE-ONLY and OUTSIDE the Edits model (ADR-0014) — a direct
// POST to /applications/{package}/dataSafety, not an edit transaction.
//
// This file ships the #141 surface: the command skeleton (flag parsing,
// --file default, --package resolution, Account resolution) and the
// --dry-run rehearsal — validate the CSV, resolve the target, and report
// "would POST N bytes / N rows to <package>" with ZERO network I/O. Because
// nothing is written, --dry-run does not require --confirm. The live POST and
// the --confirm gate land in #142.
package set

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/compliance/datasafety"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// DefaultFile is the canonical on-disk Data Safety CSV path (ADR-0014),
// shared with `compliance datasafety validate`.
const DefaultFile = "./compliance/data-safety.csv"

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	File    string
	DryRun  bool
}

// usageError is a CLI-misuse error (no package); ExitCode()=2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// fileError signals the CSV could not be read; ExitCode()=20 (client-side
// validation — the operator pointed --file at something gplay cannot read).
type fileError struct {
	file  string
	cause error
}

func (e *fileError) Error() string {
	return fmt.Sprintf("cannot read Data Safety CSV at %s: %v — pass --file <path> to point at your declaration", e.file, e.cause)
}
func (e *fileError) ExitCode() int { return 20 }
func (e *fileError) Unwrap() error { return e.cause }

// ValidationError carries the structural Problems found in the CSV by the
// implicit validate; ExitCode()=20. set refuses to POST (or rehearse a POST
// of) a structurally invalid declaration.
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

// Payload renders what set did (or, on --dry-run, would do). On a dry-run it
// is the rehearsal report: the resolved package/account and the size of the
// declaration that would be posted. The live-POST shape (the API
// pass-through) is added in #142.
type Payload struct {
	Package  string
	Account  string
	Bytes    int
	Rows     int
	DryRun   bool
	Warnings []string
}

// Renderers satisfies output.Renderable.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

// dryRunSuffix marks a previewed (not yet posted) declaration so it is never
// mistaken for a committed one.
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
		fmt.Fprintf(tw, "account\t%s\n", p.Account)
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

// jsonView is the gplay-shaped dry-run report. The live POST (#142) emits the
// API response verbatim instead (ADR-0003); a dry-run has no response to
// mirror, so this object is unmistakable from one (it carries dryRun:true).
type jsonView struct {
	DryRun   bool     `json:"dryRun"`
	Package  string   `json:"package"`
	Account  string   `json:"account,omitempty"`
	Bytes    int      `json:"bytes"`
	Rows     int      `json:"rows"`
	Warnings []string `json:"warnings,omitempty"`
}

func (p Payload) renderJSON(w io.Writer) error {
	return output.WriteJSON(w, jsonView{
		DryRun:   p.DryRun,
		Package:  p.Package,
		Account:  p.Account,
		Bytes:    p.Bytes,
		Rows:     p.Rows,
		Warnings: p.Warnings,
	})
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

// resolvePackage resolves the target package: --package wins, else the
// project pin. An empty result is a usage error.
func resolvePackage(rc *kernel.RunContext, in Input) (string, error) {
	pkg := strings.TrimSpace(in.Package)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return "", &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}
	return pkg, nil
}

// loadAndValidate reads the CSV off disk and runs the implicit structural
// validate. A read failure is exit 20 (fileError); structural problems are
// exit 20 (ValidationError). On success it returns the validator Report
// (carrying the canonical Payload, row count, and any non-fatal warnings).
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

// Run is the business function the kernel invokes. It reads + validates the
// CSV (implicitly, so an invalid declaration never reaches a POST or claims a
// successful rehearsal), resolves the target package, and — on --dry-run —
// reports the target without any network I/O.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	file := in.File
	if file == "" {
		file = DefaultFile
	}

	// Implicit validate first: a structurally invalid CSV fails before any
	// resolution work claims success (ADR-0014 / #141).
	rep, err := loadAndValidate(file)
	if err != nil {
		return nil, err
	}

	pkg, err := resolvePackage(rc, in)
	if err != nil {
		return nil, err
	}

	if in.DryRun {
		// Full rehearsal minus the POST: report the resolved target and the
		// size of the declaration. Zero network, no --confirm required.
		return Payload{
			Package:  pkg,
			Account:  rc.AccountName,
			Bytes:    len(rep.Payload),
			Rows:     rep.Rows,
			DryRun:   true,
			Warnings: rep.Warnings,
		}, nil
	}

	// The live POST and the --confirm gate land in #142. Until then, the only
	// supported invocation is --dry-run.
	return nil, &usageError{msg: "the live Data Safety write is not yet available in this build; preview it with --dry-run"}
}

// NewCommand returns the cobra command for `gplay compliance datasafety set`.
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
The declaration is write-only and replaces the whole document (ADR-0014).

set runs ` + "`validate`" + ` implicitly first, so a structurally invalid CSV
never reaches the network. Use --dry-run to rehearse the write — it validates
the CSV, resolves the target package and Account, and reports "would POST N
bytes / N rows to <package>" without any network call (and without needing
--confirm).`,
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
	return cmd
}
