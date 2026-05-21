// Package doctor implements `gplay auth doctor`: ordered diagnostic
// checks (per docs/DESIGN.md §1) that catch credential misconfiguration
// before any other command tries to talk to the API.
//
// This package is the thin command-layer glue: it resolves the active
// credential, hands it to internal/auth/doctor, and renders the
// resulting checklist via internal/output (table = emoji checklist,
// json = []CheckResult pass-through, markdown = GitHub task list).
//
// Checks 1–3 always run (issue #11 slice). For each `--package <name>`
// passed, the per-package edits.insert/delete round-trip (issue #12) is
// appended to the chain — once per package value, in the order given on
// the command line.
package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	authdoctor "github.com/PollyGlot/google-play-cli/internal/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// Options pins where the command reads state from. Output streams are
// wired via cobra's SetOut/SetErr.
type Options struct {
	ConfigPath   string
	KeystoreRoot string
	// Keyring is the keystore backend the command will probe. If nil, the
	// default go-keyring adapter is used. Tests inject a fake to keep the
	// OS keystore out of unit runs.
	Keyring keystore.KeyringAPI
}

// failedError wraps the failing CheckResult so main.go can extract the
// authoritative ExitCode for the process. When multiple checks fail
// (e.g. several --package values), `first` carries the *worst* failure
// (highest non-zero ExitCode); a tie picks the earliest in order.
type failedError struct {
	first authdoctor.CheckResult
}

func (e *failedError) Error() string {
	if e.first.Hint != "" {
		return e.first.Name + ": " + e.first.Hint
	}
	return e.first.Name + ": check failed"
}

// ExitCode extracts the exit code that should be returned to the shell
// for an error returned by the doctor command. Returns 0 when err is
// nil, the failing check's ExitCode when err is a doctor failure, and
// 1 otherwise (per docs/DESIGN.md §9 fallback).
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var fe *failedError
	if errors.As(err, &fe) {
		if fe.first.ExitCode != 0 {
			return fe.first.ExitCode
		}
		return 10
	}
	return 1
}

// NewCommand returns the cobra command for `gplay auth doctor`.
func NewCommand(opts Options) *cobra.Command {
	var (
		outputFlag string
		packages   []string
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run ordered diagnostic checks on the active credential",
		Long: `Run the doctor sequence from docs/DESIGN.md §1:

1. Service account JSON is valid
2. OAuth2 access token can be minted
3. Token carries the androidpublisher scope
4. (Per --package) edits.insert + edits.delete round-trip on the package

Checks 1–3 run once. Check 4 runs once per --package value passed (in
order). Checks run in order and the chain stops on the first failure;
subsequent checks are reported as skipped. Use --output json to get a
structured []CheckResult for scripting.`,
		// A failing doctor is a normal exit path, not a usage error;
		// silence cobra's usage banner so it does not collide with the
		// rendered checklist on stdout.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, opts, output.Format(outputFlag), packages)
		},
	}
	cmd.Flags().StringVar(&outputFlag, "output", "", "output format: table, json, or markdown (default: auto — table on TTY, json in pipes/CI)")
	cmd.Flags().StringSliceVar(&packages, "package", nil,
		"Android package name to round-trip an edits.insert+delete against; repeat for multiple packages")
	return cmd
}

func run(cmd *cobra.Command, opts Options, format output.Format, packages []string) error {
	results, worstFailure := executeChecks(cmd, opts, packages)

	if err := output.Render(cmd.OutOrStdout(), format, renderersFor(results)); err != nil {
		return err
	}
	if worstFailure != nil {
		return &failedError{first: *worstFailure}
	}
	return nil
}

// buildChecks composes the ordered Check chain for this invocation:
// the three non-API checks, plus one per-package check per --package
// value (in command-line order).
func buildChecks(packages []string) []authdoctor.Check {
	checks := authdoctor.DefaultChecks()
	for _, p := range packages {
		checks = append(checks, authdoctor.CheckPackageAccess(p))
	}
	return checks
}

// executeChecks resolves the active credential and runs the doctor
// chain. If resolution fails, a synthetic check-1 failure is produced
// and subsequent checks are reported as Skipped — so the user always
// sees an ordered checklist instead of an opaque pre-check error.
//
// The worst failure (highest non-zero ExitCode; earliest wins on ties)
// is returned alongside the full result slice, so the command's exit
// code surfaces the most actionable problem from a multi-package run.
func executeChecks(cmd *cobra.Command, opts Options, packages []string) ([]authdoctor.CheckResult, *authdoctor.CheckResult) {
	checks := buildChecks(packages)
	resolved, err := config.LoadFromEnv(opts.ConfigPath)
	if err != nil {
		return synthFailure(err, checks)
	}
	kr := opts.Keyring
	if kr == nil {
		kr = keystore.DefaultKeyring()
	}
	be, _, err := keystore.Select(keystore.SelectOptions{
		Keyring:  kr,
		FileRoot: opts.KeystoreRoot,
	})
	if err != nil {
		return synthFailure(err, checks)
	}
	sa, err := resolver.New(resolved, be).Resolve(resolver.Inputs{})
	if err != nil {
		return synthFailure(err, checks)
	}

	results := authdoctor.Run(cmd.Context(), sa, nil, checks...)
	return results, worstFailure(results)
}

// worstFailure returns a pointer to the CheckResult with the highest
// non-zero ExitCode in results, ignoring Skipped entries. Ties are
// broken by the earliest position in the slice (so a 403 on package #1
// wins over a 403 on package #3 — same exit code, but the first one
// reported is the most natural "first thing to look at").
func worstFailure(results []authdoctor.CheckResult) *authdoctor.CheckResult {
	var worst *authdoctor.CheckResult
	for i := range results {
		r := &results[i]
		if r.Passed || r.Skipped {
			continue
		}
		if worst == nil || r.ExitCode > worst.ExitCode {
			worst = r
		}
	}
	return worst
}

// synthFailure builds a result slice where check #1 fails with the
// resolution error's hint and checks #2..N are reported as Skipped, so
// the JSON output and TTY rendering stay consistent with the
// stop-on-first-failure rule even when resolution itself died.
func synthFailure(err error, checks []authdoctor.Check) ([]authdoctor.CheckResult, *authdoctor.CheckResult) {
	failure := authdoctor.ResolutionFailure(err)[0]
	results := make([]authdoctor.CheckResult, 0, len(checks))
	results = append(results, failure)
	for i := 1; i < len(checks); i++ {
		results = append(results, authdoctor.NewSkippedResult(checks[i]))
	}
	return results, &results[0]
}

// iconForResult picks the emoji shown next to a check's Name in the
// table-mode output. Emoji belong to the table form only; the markdown
// renderer uses the `- [x] / - [ ]` task-list idiom instead.
func iconForResult(r authdoctor.CheckResult) string {
	switch {
	case r.Skipped:
		return "⏭"
	case !r.Passed:
		return "❌"
	default:
		return "✅"
	}
}

// renderersFor wires the three Format renderers for a slice of check
// results.
func renderersFor(results []authdoctor.CheckResult) output.Renderers {
	return output.Renderers{
		Table: func(w io.Writer) error {
			for _, r := range results {
				if _, err := fmt.Fprintf(w, "%s  %s\n", iconForResult(r), r.Name); err != nil {
					return err
				}
				if !r.Passed && !r.Skipped && r.Hint != "" {
					if _, err := fmt.Fprintf(w, "   hint: %s\n", r.Hint); err != nil {
						return err
					}
				}
			}
			return nil
		},
		JSON: func(w io.Writer) error {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(results)
		},
		Markdown: func(w io.Writer) error { return renderMarkdownChecklist(w, results) },
	}
}

// renderMarkdownChecklist emits a GitHub-flavored task list. Passed checks
// → `- [x] name`. Failed checks → `- [ ] name — hint: <hint>` (hint
// dropped when empty). Skipped checks → `- [ ] _skipped_: name` so a
// reader can scan failures and skips separately.
func renderMarkdownChecklist(w io.Writer, results []authdoctor.CheckResult) error {
	for _, r := range results {
		var line string
		switch {
		case r.Skipped:
			line = fmt.Sprintf("- [ ] _skipped_: %s\n", r.Name)
		case !r.Passed:
			if r.Hint != "" {
				line = fmt.Sprintf("- [ ] %s — hint: %s\n", r.Name, r.Hint)
			} else {
				line = fmt.Sprintf("- [ ] %s\n", r.Name)
			}
		default:
			line = fmt.Sprintf("- [x] %s\n", r.Name)
		}
		if _, err := fmt.Fprint(w, line); err != nil {
			return err
		}
	}
	return nil
}
