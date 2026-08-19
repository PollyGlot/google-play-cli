// Package set implements `gplay compliance datasafety set`: it pushes the
// app's Data Safety declaration to Google from the canonical CSV. The
// declaration is WRITE-ONLY and OUTSIDE the Edits model (ADR-0014): a direct
// POST to /applications/{package}/dataSafety, not an edit transaction.
//
// set runs `validate` implicitly first, so a structurally invalid CSV never
// reaches the network. Two paths (ADR-0014):
//
//   - --dry-run is the full rehearsal MINUS the POST: validate + resolve the
//     target package/account + report "would POST N bytes / N rows". Zero
//     network I/O, and it does not require --confirm (nothing is written).
//   - the real write requires --confirm (a bad declaration blocks releases or
//     misstates data practices). Without it set refuses, exits 3 (safety flag
//     required, docs/DESIGN.md §9), and points at --dry-run. CI=true NEVER
//     auto-confirms: CI governs output format only, never mutation.
package set

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/compliance/datasafety"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	playds "github.com/PollyGlot/google-play-cli/internal/play/datasafety"
)

// DefaultFile is the canonical on-disk Data Safety CSV path (ADR-0014),
// shared with `compliance datasafety validate`.
const DefaultFile = "./compliance/data-safety.csv"

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	File    string
	DryRun  bool
	Confirm bool
}

// usageError is a CLI-misuse error (no package); ExitCode()=2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// confirmRequired builds the refusal returned when a real write is invoked
// without --confirm. It is an *exit.SafetyFlagError, so it exits 3 (safety
// flag required, docs/DESIGN.md §9) and names the missing flag in the
// --output json envelope's requires[]: the same shape every other gated
// command uses (ADR-0017). It points at --dry-run and reuses gplay's
// --confirm meaning (ADR-0002 / ADR-0011 item 4, recorded in ADR-0014).
// CI=true must never reach this as an auto-confirm: env governs output
// format, not mutation.
func confirmRequired() error {
	return exit.SafetyFlag("confirm", "compliance datasafety set replaces the app's live Data Safety declaration (a stale or wrong declaration can block releases); pass --confirm to proceed (preview first with --dry-run)")
}

// packageNotFoundError / forbiddenError attach actionable hints to a 404 /
// 403 on the POST, leaving the wrapped *api.Error to drive the exit code
// (404→30, 403→11): mirroring `testers set` / `metadata apply`.
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

// classifyError adds the 404/403 hints to a dataSafety POST failure, leaving
// the wrapped *api.Error to drive the exit code. Every other failure (5xx,
// network) propagates verbatim.
func classifyError(pkg string, err error) error {
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

// fileError signals the CSV could not be read; ExitCode()=20 (client-side
// validation: the operator pointed --file at something gplay cannot read).
type fileError struct {
	file  string
	cause error
}

func (e *fileError) Error() string {
	return fmt.Sprintf("cannot read Data Safety CSV at %s: %v: pass --file <path> to point at your declaration", e.file, e.cause)
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
// declaration that would be posted. On a real write Raw carries the
// dataSafety POST response body for the ADR-0003 --output json pass-through
// (which may be empty: see renderJSON).
type Payload struct {
	Package  string
	Account  string
	Bytes    int
	Rows     int
	DryRun   bool
	Warnings []string
	Raw      json.RawMessage
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
	// Real write: pass the API response through verbatim (ADR-0003). The
	// dataSafety POST may return an EMPTY body on success, in which case
	// there is nothing to pass through: a documented exception to ADR-0003.
	// We emit a gplay-shaped success object instead of zero bytes so a CI
	// pipeline (where --output json is the default) always gets parseable
	// output rather than an empty stream.
	if len(bytes.TrimSpace(p.Raw)) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, struct {
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

// resolvePackage resolves the target package: --package wins, else the
// project pin. An empty result is a usage error.
func resolvePackage(rc *kernel.RunContext, in Input) (string, error) {
	pkg := strings.TrimSpace(in.Package)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return "", &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
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
// successful rehearsal), resolves the target package, and, on --dry-run,
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

	// Confirm gate (real writes only). Evaluated before any network so a
	// missing --confirm fails instantly, never after a POST. CI=true does NOT
	// flow into in.Confirm: env governs output format, never mutation.
	if !in.Confirm {
		return nil, confirmRequired()
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	raw, err := playds.Post(rc.Ctx, httpClient, pkg, rep.Payload)
	if err != nil {
		return nil, classifyError(pkg, err)
	}

	// DESIGN §8: a committed mutation prints one ✓ line on stderr. The
	// --dry-run path returned above, so this only runs after a real POST.
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
The declaration is write-only and replaces the whole document: a direct POST
outside the Edits model (ADR-0014). gplay cannot read it back; only this POST
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
