// Package rollout implements the staged-rollout state machine as four
// sibling commands — `gplay releases rollout / halt / resume / complete`.
// Each is thin glue: it resolves --package, validates flags, builds an
// authenticated HTTP client from the active Account, and hands a StateOpts
// to internal/releases/orchestrator. The four share this file's Input,
// renderers, and run helper; one file per verb carries the cobra wiring.
package rollout

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// Input is the request-shaped struct cobra builds from flags. All four
// verbs share it; To / ToSet are read only by rollout. To is the raw
// --to flag value (parsed by RunRollout) so a non-numeric value yields a
// CLI-misuse exit 2 with a clear hint, rather than cobra's exit-1 parse error.
type Input struct {
	Package           string
	Track             string
	VersionCode       int
	ReleaseName       string
	To                string
	ToSet             bool
	KeepEditOnFailure bool
	Confirm           bool
	DryRun            bool
}

// usageError is a CLI-misuse error with ExitCode()=2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Payload satisfies output.Renderable for the resulting state-transition
// Result. JSON is API pass-through (the raw tracks.update body, ADR-0003).
type Payload struct {
	Result *orchestrator.Result
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Result) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p.Result) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Result) },
	}
}

// showsUserFraction reports whether userFraction is worth printing. A live
// inProgress / halted release always carries a non-zero fraction, so a
// positive value is the signal — that also hides the synthetic zero a
// halt / resume --dry-run preview leaves behind (those preserve the live
// fraction, which is unknown without an API call) rather than implying 0%.
func showsUserFraction(userFraction float64) bool {
	return userFraction > 0
}

func renderTable(w io.Writer, r *orchestrator.Result) error {
	if _, err := fmt.Fprintf(w,
		"versionCode:  %d\ntrack:        %s\nstatus:       %s\n",
		r.VersionCode, r.Track, r.Status,
	); err != nil {
		return err
	}
	if showsUserFraction(r.UserFraction) {
		if _, err := fmt.Fprintf(w, "userFraction: %v\n", r.UserFraction); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "releaseName:  %s\n", r.ReleaseName)
	return err
}

func renderJSON(w io.Writer, r *orchestrator.Result) error {
	// API pass-through: emit the raw tracks.update body (ADR-0003).
	if len(r.RawTrackResponse) > 0 {
		_, err := w.Write(r.RawTrackResponse)
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func renderMarkdown(w io.Writer, r *orchestrator.Result) error {
	if _, err := fmt.Fprintf(w,
		"- **versionCode**: %d\n- **track**: %s\n- **status**: %s\n",
		r.VersionCode, r.Track, r.Status,
	); err != nil {
		return err
	}
	if showsUserFraction(r.UserFraction) {
		if _, err := fmt.Fprintf(w, "- **userFraction**: %v\n", r.UserFraction); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "- **releaseName**: %s\n", r.ReleaseName)
	return err
}

// stateFunc is the orchestrator entry point shape shared by Rollout / Halt
// / Resume / Complete, so runState can drive any of the four uniformly.
type stateFunc func(ctx context.Context, hc *http.Client, opts orchestrator.StateOpts) (*orchestrator.Result, error)

// runState is the glue common to every verb: resolve the package, require
// a track, build the authenticated client (skipped in dry-run), and invoke
// the chosen orchestrator function. userFraction is meaningful only for
// rollout (0 for halt / resume / complete, which the orchestrator ignores).
// Per-verb validation (rollout's --to) runs in the verb's own Run before this.
// action is the past-tense transition word ("set" / "halted" / "resumed" /
// "completed") for the ✓ confirmation line.
func runState(rc *kernel.RunContext, in Input, userFraction float64, action string, call stateFunc) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}
	if in.Track == "" {
		return nil, &usageError{msg: "missing --track"}
	}

	// Dry-run skips auth entirely: nothing hits the network, so a missing
	// Account is not a problem. The orchestrator handles the dry-run path
	// before any HTTP would happen.
	var httpClient *http.Client
	if !in.DryRun {
		var err error
		httpClient, err = rc.AuthedClient()
		if err != nil {
			return nil, err
		}
	}

	// Reuse an open explicit Edit when one is pinned (`gplay edits begin`); ""
	// keeps the implicit per-transition Edit.
	explicitEditID, err := rc.ExplicitEditID(pkg)
	if err != nil {
		return nil, err
	}

	result, err := call(rc.Ctx, httpClient, orchestrator.StateOpts{
		Package:           pkg,
		Track:             in.Track,
		VersionCode:       in.VersionCode,
		ReleaseName:       in.ReleaseName,
		UserFraction:      userFraction,
		KeepEditOnFailure: in.KeepEditOnFailure,
		ExplicitEditID:    explicitEditID,
		Confirm:           in.Confirm,
		DryRun:            in.DryRun,
	})
	if err != nil {
		return nil, err
	}
	// DESIGN §8: a committed transition prints one ✓ line on stderr (never on a
	// --dry-run). userFraction is shown only when status is inProgress.
	if !in.DryRun {
		extra := ""
		if result.Status == "inProgress" {
			extra = ", userFraction " + output.Percent(result.UserFraction)
		}
		rc.Confirmf("rollout %s on track %q (versionCode %d, status %s%s)",
			action, in.Track, result.VersionCode, result.Status, extra)
	}
	return Payload{Result: result}, nil
}

// RunRollout validates the --to fraction (AC6: required, numeric, in
// (0, 1] — every misuse is exit 2 with a range hint) then drives
// orchestrator.Rollout.
func RunRollout(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if !in.ToSet {
		return nil, &usageError{msg: "missing --to: gplay releases rollout --to <fraction> (0 < f ≤ 1.0)"}
	}
	fraction, err := strconv.ParseFloat(strings.TrimSpace(in.To), 64)
	if err != nil {
		return nil, &usageError{msg: "--to must be a number in (0, 1] (e.g. 0.05, 0.20)"}
	}
	if fraction <= 0 || fraction > 1.0 {
		return nil, &usageError{msg: "--to fraction must be in (0, 1] (e.g. 0.05, 0.20)"}
	}
	return runState(rc, in, fraction, "set", orchestrator.Rollout)
}

// bindCommonFlags registers the flags every verb shares (output, package,
// track, the two disambiguators, keep-edit-on-failure, dry-run).
func bindCommonFlags(cmd *cobra.Command, in *Input, outputFlag *string) {
	output.RegisterFlag(cmd, outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Track, "track", "", "target track (internal, alpha, beta, production, or any closed-track name)")
	cmd.Flags().IntVar(&in.VersionCode, "version-code", 0, "pick the release with this versionCode (disambiguator when the track holds more than one)")
	cmd.Flags().StringVar(&in.ReleaseName, "release-name", "", "pick the release with this name (disambiguator)")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "required to roll out / resume / complete a release on production (reaches real users)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the transition without any HTTP call")
}

// newStateCommand builds a cobra command for a no-extra-flags verb (halt /
// resume / complete). rollout has its own constructor because of --to.
func newStateCommand(boot kernel.Boot, use, short, long string, run func(*kernel.RunContext, Input) (output.Renderable, error)) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:           use,
		Short:         short,
		Long:          long,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return run(rc, in)
			})
		},
	}
	bindCommonFlags(cmd, &in, &outputFlag)
	return cmd
}

// NewRolloutCommand returns the cobra command for `gplay releases rollout`.
func NewRolloutCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "rollout",
		Short: "Set the staged-rollout fraction on the latest release of a track",
		Long: `Set the staged-rollout fraction (--to) on the latest release of --track.
Status becomes inProgress if it wasn't already.

Targets the latest release on the track; when two releases coexist (e.g.
inProgress + halted) pass --version-code N or --release-name <name> to pick
one, otherwise the command refuses rather than guess.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.ToSet = cmd.Flags().Changed("to")
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return RunRollout(rc, in)
			})
		},
	}
	bindCommonFlags(cmd, &in, &outputFlag)
	cmd.Flags().StringVar(&in.To, "to", "", "target rollout fraction (0 < f ≤ 1.0), e.g. 0.05")
	return cmd
}
