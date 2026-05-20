// Package doctor implements `gplay auth doctor`: ordered diagnostic
// checks (per docs/DESIGN.md §1) that catch credential misconfiguration
// before any other command tries to talk to the API.
//
// This package is the thin command-layer glue: it resolves the active
// credential, hands it to internal/auth/doctor, and renders either a
// TTY checklist or a JSON pass-through of []CheckResult.
//
// Scope of this slice (issue #11): non-API checks only —
//  1. SA JSON validity
//  2. OAuth2 access-token mint
//  3. token carries the androidpublisher scope
//
// The per-package edits.insert/delete round-trip (issue #12) plugs in
// as one more entry in doctor.DefaultChecks() without touching this
// glue.
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
)

// Options pins where the command reads state from. Output streams are
// wired via cobra's SetOut/SetErr.
type Options struct {
	ConfigPath   string
	KeystoreRoot string
}

// failedError wraps the failing CheckResult so main.go can extract the
// authoritative ExitCode for the process.
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
	var output string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run ordered diagnostic checks on the active credential",
		Long: `Run the doctor sequence from docs/DESIGN.md §1:

1. Service account JSON is valid
2. OAuth2 access token can be minted
3. Token carries the androidpublisher scope

Checks run in order and the chain stops on the first failure;
subsequent checks are reported as skipped. Use --output json to get a
structured []CheckResult for scripting.`,
		// A failing doctor is a normal exit path, not a usage error;
		// silence cobra's usage banner so it does not collide with the
		// rendered checklist on stdout.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, opts, output)
		},
	}
	cmd.Flags().StringVar(&output, "output", "table", "output format: table or json")
	return cmd
}

func run(cmd *cobra.Command, opts Options, output string) error {
	results, firstFailure := executeChecks(cmd, opts)

	if err := render(cmd.OutOrStdout(), output, results); err != nil {
		return err
	}
	if firstFailure != nil {
		return &failedError{first: *firstFailure}
	}
	return nil
}

// executeChecks resolves the active credential and runs the doctor
// chain. If resolution fails, a synthetic check-1 failure is produced
// and subsequent checks are reported as Skipped — so the user always
// sees an ordered checklist instead of an opaque pre-check error.
func executeChecks(cmd *cobra.Command, opts Options) ([]authdoctor.CheckResult, *authdoctor.CheckResult) {
	cfg, err := config.LoadOrEmpty(opts.ConfigPath)
	if err != nil {
		return synthFailure(err, len(authdoctor.DefaultChecks()))
	}
	be := keystore.NewFileBackend(opts.KeystoreRoot)
	sa, err := resolver.New(cfg, be).Resolve(resolver.Inputs{})
	if err != nil {
		return synthFailure(err, len(authdoctor.DefaultChecks()))
	}

	results := authdoctor.Run(cmd.Context(), sa, nil, authdoctor.DefaultChecks()...)
	for i := range results {
		if !results[i].Passed {
			return results, &results[i]
		}
	}
	return results, nil
}

// synthFailure builds a result slice where check #1 fails with the
// resolution error's hint and checks #2..N are reported as Skipped, so
// the JSON output and TTY rendering stay consistent with the
// stop-on-first-failure rule even when resolution itself died.
func synthFailure(err error, total int) ([]authdoctor.CheckResult, *authdoctor.CheckResult) {
	failure := authdoctor.ResolutionFailure(err)[0]
	results := make([]authdoctor.CheckResult, 0, total)
	results = append(results, failure)
	defaults := authdoctor.DefaultChecks()
	for i := 1; i < total && i < len(defaults); i++ {
		results = append(results, authdoctor.CheckResult{
			Name:     defaults[i].Name,
			Passed:   false,
			Skipped:  true,
			ExitCode: defaults[i].ExitCode,
		})
	}
	return results, &results[0]
}

func render(w io.Writer, output string, results []authdoctor.CheckResult) error {
	switch output {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	case "table":
		for _, r := range results {
			icon := "✅"
			switch {
			case r.Skipped:
				icon = "⏭"
			case !r.Passed:
				icon = "❌"
			}
			if _, err := fmt.Fprintf(w, "%s  %s\n", icon, r.Name); err != nil {
				return err
			}
			if !r.Passed && !r.Skipped && r.Hint != "" {
				if _, err := fmt.Fprintf(w, "   hint: %s\n", r.Hint); err != nil {
					return err
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported --output %q (want table or json)", output)
	}
}
