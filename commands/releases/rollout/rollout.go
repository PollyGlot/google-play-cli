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
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

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

type Payload struct {
	Result *orchestrator.Result
	DryRun bool
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
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Result) },
	}
}

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

type planJSON struct {
	Plan bool `json:"plan"`
	*orchestrator.TransitionPlan
}

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

func joinCodes(codes []int) string {
	s := make([]string, len(codes))
	for i, c := range codes {
		s[i] = strconv.Itoa(c)
	}
	return strings.Join(s, ", ")
}

type dryRunJSON struct {
	DryRun bool `json:"dryRun"`
	*orchestrator.Result
	Requires []string `json:"requires"`
}

func renderJSON(w io.Writer, p Payload) error {
	r := p.Result
	if len(r.RawTrackResponse) > 0 {
		_, err := w.Write(r.RawTrackResponse)
		return err
	}
	if p.DryRun {
		return output.WriteJSON(w, dryRunJSON{DryRun: true, Result: r, Requires: output.NonNil(r.Requires)})
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

type stateFunc func(ctx context.Context, hc *http.Client, opts orchestrator.StateOpts) (*orchestrator.Result, error)

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

	var (
		httpClient     *http.Client
		explicitEditID string
	)
	if !in.DryRun {
		var err error
		if httpClient, err = rc.AuthedClient(); err != nil {
			return nil, err
		}
		if explicitEditID, err = rc.ExplicitEditID(pkg); err != nil {
			return nil, err
		}
	}

	var commit edits.CommitOptions
	if !in.Plan {
		commit = in.Commit.For(rc, explicitEditID)
	}
	result, err := call(rc.Ctx, httpClient, orchestrator.StateOpts{
		Package:           pkg,
		Track:             in.Track,
		VersionCode:       in.VersionCode,
		ReleaseName:       in.ReleaseName,
		UserFraction:      userFraction,
		KeepEditOnFailure: in.KeepEditOnFailure,
		ExplicitEditID:    explicitEditID,
		Commit:            commit,
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
	if !in.DryRun {
		extra := ""
		if result.Status == tracks.StatusInProgress {
			extra = ", userFraction " + output.Percent(result.UserFraction)
		}
		if len(result.SupersededVersionCodes) > 0 {
			extra += ", superseded versionCode " + joinCodes(result.SupersededVersionCodes)
		}
		rc.ConfirmMutation(explicitEditID, "rollout %s on track %q (versionCode %d, status %s%s)",
			action, in.Track, result.VersionCode, result.Status, extra)
	}
	return Payload{Result: result, DryRun: in.DryRun}, nil
}

func RunRollout(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if !in.StagedFractionSet {
		return nil, exit.Usagef("missing --staged: pass --staged <fraction> (0 < f < 1, e.g. 0.05)")
	}
	if _, err := orchestrator.InProgress(in.StagedFraction); err != nil {
		return nil, exit.Usagef("--staged: %v; for a full rollout run gplay releases complete", err)
	}
	return runState(rc, in, in.StagedFraction, "set", orchestrator.Rollout)
}

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
