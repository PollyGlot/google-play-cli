// Package rollout implements the staged-rollout state machine as four
// sibling commands: `gplay releases rollout / halt / resume / complete`.
// Each is thin glue: it resolves --package, validates flags, builds an
// authenticated HTTP client from the active Account, and hands a StateOpts
// to internal/releases/orchestrator. The four share this file's Input,
// renderers, and run helper; one file per verb carries the cobra wiring.
package rollout

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// Input is the request-shaped struct cobra builds from flags. All four
// verbs share it; StagedFraction / StagedFractionSet are read only by rollout.
// The fraction is --staged, the name `releases upload` and `releases promote`
// give the same userFraction (#597); a non-numeric value fails flag parsing,
// which is already a CLI-misuse exit 2.
type Input struct {
	Package           string
	Track             string
	VersionCode       int
	ReleaseName       string
	StagedFraction    float64
	StagedFractionSet bool
	KeepEditOnFailure bool
	Commit            commitflags.Flags
	Confirm           bool
	DryRun            bool
	Plan              bool
}

// Payload satisfies output.Renderable for the resulting state-transition
// Result. JSON is API pass-through (the raw tracks.update body, ADR-0003).
type Payload struct {
	Result *orchestrator.Result
}

func (p Payload) Renderers() output.Renderers {
	if plan := p.Result.Plan; plan != nil {
		return output.Renderers{
			Table:    func(w io.Writer) error { return renderPlan(w, plan, "%s:%s%s\n") },
			JSON:     func(w io.Writer) error { return output.WriteJSON(w, planJSON{Plan: true, TransitionPlan: plan}) },
			Markdown: func(w io.Writer) error { return renderPlan(w, plan, "- **%[1]s**: %[3]s\n") },
		}
	}
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Result) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p.Result) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Result) },
	}
}

// showsUserFraction reports whether userFraction is worth printing. A live
// inProgress / halted release always carries a non-zero fraction, so a
// positive value is the signal: that also hides the synthetic zero a
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
	if _, err := fmt.Fprintf(w, "releaseName:  %s\n", r.ReleaseName); err != nil {
		return err
	}
	if len(r.SupersededVersionCodes) > 0 {
		if _, err := fmt.Fprintf(w, "superseded:   %s\n", joinCodes(r.SupersededVersionCodes)); err != nil {
			return err
		}
	}
	return nil
}

// planJSON is the --plan JSON shape: gplay-authored, never a tracks.update
// body, and marked "plan": true so a script cannot mistake it for a result.
type planJSON struct {
	Plan bool `json:"plan"`
	*orchestrator.TransitionPlan
}

// renderPlan writes one labelled line per plan field through line, a format
// given the label, the table padding after its colon, and the value.
func renderPlan(w io.Writer, p *orchestrator.TransitionPlan, line string) error {
	becomes := p.Status
	if showsUserFraction(p.UserFraction) {
		becomes += fmt.Sprintf(", userFraction %v", p.UserFraction)
	}
	rows := [][2]string{
		{"track", p.Track},
		{"target", describeRelease(p.Target)},
		{"becomes", becomes},
		{"superseded", describeReleases(p.Superseded)},
		{"kept", describeReleases(p.Kept)},
	}
	for _, r := range rows {
		pad := strings.Repeat(" ", 11-len(r[0]))
		if _, err := fmt.Fprintf(w, line, r[0], pad, r[1]); err != nil {
			return err
		}
	}
	return nil
}

// describeRelease renders a release as "2.4.0 [versionCodes 143], inProgress,
// userFraction 0.2".
func describeRelease(r tracks.Release) string {
	name := r.Name
	if name == "" {
		name = "(unnamed)"
	}
	s := fmt.Sprintf("%s [versionCodes %s], %s", name, strings.Join(r.VersionCodes, ", "), r.Status)
	if showsUserFraction(r.UserFraction) {
		s += fmt.Sprintf(", userFraction %v", r.UserFraction)
	}
	return s
}

func describeReleases(rs []tracks.Release) string {
	if len(rs) == 0 {
		return "none"
	}
	s := make([]string, len(rs))
	for i, r := range rs {
		s[i] = describeRelease(r)
	}
	return strings.Join(s, "; ")
}

// joinCodes renders versionCodes as "140, 141".
func joinCodes(codes []int) string {
	s := make([]string, len(codes))
	for i, c := range codes {
		s[i] = strconv.Itoa(c)
	}
	return strings.Join(s, ", ")
}

func renderJSON(w io.Writer, r *orchestrator.Result) error {
	// API pass-through: emit the raw tracks.update body (ADR-0003).
	if len(r.RawTrackResponse) > 0 {
		_, err := w.Write(r.RawTrackResponse)
		return err
	}
	return output.WriteJSON(w, r)
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
	if _, err := fmt.Fprintf(w, "- **releaseName**: %s\n", r.ReleaseName); err != nil {
		return err
	}
	if len(r.SupersededVersionCodes) > 0 {
		if _, err := fmt.Fprintf(w, "- **superseded**: %s\n", joinCodes(r.SupersededVersionCodes)); err != nil {
			return err
		}
	}
	return nil
}

// stateFunc is the orchestrator entry point shape shared by Rollout / Halt
// / Resume / Complete, so runState can drive any of the four uniformly.
type stateFunc func(ctx context.Context, hc *http.Client, opts orchestrator.StateOpts) (*orchestrator.Result, error)

// runState is the glue common to every verb: resolve the package, require
// a track, build the authenticated client (skipped in dry-run), and invoke
// the chosen orchestrator function. userFraction is meaningful only for
// rollout (0 for halt / resume / complete, which the orchestrator ignores).
// Per-verb validation (rollout's --staged) runs in the verb's own Run before this.
// action is the past-tense transition word ("set" / "halted" / "resumed" /
// "completed") for the ✓ confirmation line.
func runState(rc *kernel.RunContext, in Input, userFraction float64, action string, call stateFunc) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	if in.Plan && in.DryRun {
		return nil, exit.Usagef("--plan and --dry-run are mutually exclusive: --dry-run stays offline, --plan reads the live track")
	}
	if in.Track == "" {
		return nil, exit.Usagef("missing --track: pass --track <name> (internal, alpha, beta, production, or any closed-track name)")
	}

	// Dry-run skips auth entirely: nothing hits the network, so a missing
	// Account is not a problem. The orchestrator handles the dry-run path
	// before any HTTP would happen.
	// Dry-run skips auth AND the explicit-Edit pin: nothing hits the network and
	// the dry-run path never reuses a pinned Edit, so a corrupt pin must not
	// fail a preview. Both are resolved only on the live path.
	var (
		httpClient     *http.Client
		explicitEditID string
	)
	if !in.DryRun {
		var err error
		if httpClient, err = rc.AuthedClient(); err != nil {
			return nil, err
		}
		// Reuse an open explicit Edit when one is pinned (`gplay edits begin`);
		// "" keeps the implicit per-transition Edit.
		if explicitEditID, err = rc.ExplicitEditID(pkg); err != nil {
			return nil, err
		}
	}

	result, err := call(rc.Ctx, httpClient, orchestrator.StateOpts{
		Package:           pkg,
		Track:             in.Track,
		VersionCode:       in.VersionCode,
		ReleaseName:       in.ReleaseName,
		UserFraction:      userFraction,
		KeepEditOnFailure: in.KeepEditOnFailure,
		ExplicitEditID:    explicitEditID,
		Commit:            in.Commit.For(rc, explicitEditID),
		Confirm:           in.Confirm,
		DryRun:            in.DryRun,
		Plan:              in.Plan,
	})
	if err != nil {
		return nil, err
	}
	if in.Plan {
		rc.Notef("plan only: nothing was sent; run again without --plan to apply")
		return Payload{Result: result}, nil
	}
	// DESIGN §8: a committed transition prints one ✓ line on stderr (never on a
	// --dry-run or a --plan). userFraction is shown only when status is inProgress.
	if !in.DryRun {
		extra := ""
		if result.Status == "inProgress" {
			extra = ", userFraction " + output.Percent(result.UserFraction)
		}
		if len(result.SupersededVersionCodes) > 0 {
			extra += ", superseded versionCode " + joinCodes(result.SupersededVersionCodes)
		}
		rc.ConfirmMutation(explicitEditID, "rollout %s on track %q (versionCode %d, status %s%s)",
			action, in.Track, result.VersionCode, result.Status, extra)
	}
	return Payload{Result: result}, nil
}

// RunRollout validates the --staged fraction (AC6: required, strictly between
// 0 and 1: every misuse is exit 2 with a range hint) then drives
// orchestrator.Rollout. A full rollout is `gplay releases complete`.
func RunRollout(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if !in.StagedFractionSet {
		return nil, exit.Usagef("missing --staged: pass --staged <fraction> (0 < f < 1, e.g. 0.05)")
	}
	if _, err := orchestrator.InProgress(in.StagedFraction); err != nil {
		return nil, exit.Usagef("--staged: %v; for a full rollout run gplay releases complete", err)
	}
	return runState(rc, in, in.StagedFraction, "set", orchestrator.Rollout)
}

// bindCommonFlags registers the flags every verb shares (output, package,
// track, the two disambiguators, keep-edit-on-failure, the commit opt-ins,
// dry-run).
func bindCommonFlags(cmd *cobra.Command, in *Input, outputFlag *string) {
	output.RegisterFlag(cmd, outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Track, "track", "", "target track (internal, alpha, beta, production, or any closed-track name) (required)")
	cmd.Flags().IntVar(&in.VersionCode, "version-code", 0, "pick the release with this versionCode (disambiguator when the track holds more than one)")
	cmd.Flags().StringVar(&in.ReleaseName, "release-name", "", "pick the release with this name (disambiguator)")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	commitflags.Register(cmd, &in.Commit)
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "required to roll out / resume / complete a release on production (reaches real users)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the transition without any HTTP call")
	cmd.Flags().BoolVar(&in.Plan, "plan", false, "read the live track and show the release this targets, the state it ends in, and the releases it supersedes or keeps, without changing anything (needs an Account)")
	kernel.ExperimentalFlag(cmd, "plan")
}

// newStateCommand builds a cobra command for a no-extra-flags verb (halt /
// resume / complete). rollout has its own constructor because of --staged.
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
		Long: `Set the staged-rollout fraction (--staged) on the latest release of --track.
Status becomes inProgress if it wasn't already.

Targets the latest release on the track; when two releases coexist (e.g.
inProgress + halted) pass --version-code N or --release-name <name> to pick
one, otherwise the command refuses rather than guess. --plan reads the
track and shows that choice without changing anything.`,
		Example: `  # Widen the production rollout to 20% of users
  gplay releases rollout --track production --staged 0.2 --confirm

  # Preview the change on one of two coexisting releases
  gplay releases rollout --track production --staged 0.5 --version-code 1042 --dry-run

  # Read the track first: which release moves to 50%, which stay as they are
  gplay releases rollout --track production --staged 0.5 --plan`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.StagedFractionSet = cmd.Flags().Changed("staged")
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return RunRollout(rc, in)
			})
		},
	}
	bindCommonFlags(cmd, &in, &outputFlag)
	cmd.Flags().Float64Var(&in.StagedFraction, "staged", 0, "target rollout fraction (0 < f < 1), e.g. 0.05 (required; for 100% use gplay releases complete)")
	return cmd
}
