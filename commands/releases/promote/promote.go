// Package promote implements `gplay releases promote`: the CLI glue
// that parses --from/--to plus the status overrides (per ADR-0002),
// resolves --package, and hands a PromoteOpts payload to
// internal/releases/orchestrator. All the HTTP / Edit-lifecycle work
// lives in the orchestrator and its internal/play/* dependencies.
package promote

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/commands/releases/trackhint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package           string
	FromTrack         string
	ToTrack           string
	VersionCode       int
	ReleaseName       string
	ReleaseNotes      string
	ReleaseNotesSet   bool // explicit `--release-notes ""` is an empty note, refused (#786)
	ReleaseNotesDir   string
	Draft             bool
	Complete          bool
	StagedFraction    float64
	StagedFractionSet bool
	// UpdatePriority is --update-priority (0..5); UpdatePrioritySet tells an
	// explicit 0 (lowest priority) apart from "not passed".
	UpdatePriority    int
	UpdatePrioritySet bool
	KeepEditOnFailure bool
	Commit            commitflags.Flags
	Confirm           bool
	DryRun            bool
	Plan              bool
}

// Payload satisfies output.Renderable for the resulting promote Result.
// Reuses the orchestrator's Result so JSON pass-through (ADR-0003) and
// the table renderer match upload's shape. DryRun marks the --dry-run
// preview, which has no body to pass through.
type Payload struct {
	Result *orchestrator.Result
	DryRun bool
}

func (p Payload) Renderers() output.Renderers {
	if plan := p.Result.Promotion; plan != nil {
		return output.Renderers{
			Table:    func(w io.Writer) error { return renderPlan(w, plan, "%s:%s%s\n") },
			JSON:     func(w io.Writer) error { return output.WriteJSON(w, planJSON{Plan: true, PromotePlan: plan}) },
			Markdown: func(w io.Writer) error { return renderPlan(w, plan, "- **%[1]s**: %[3]s\n") },
		}
	}
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// planJSON is the --plan JSON shape: gplay-authored, never a tracks.update
// body, and marked "plan": true so a script cannot mistake it for a result.
type planJSON struct {
	Plan bool `json:"plan"`
	*orchestrator.PromotePlan
}

// renderPlan writes one labelled line per plan field through line, a format
// given the label, the table padding after its colon, and the value.
func renderPlan(w io.Writer, p *orchestrator.PromotePlan, line string) error {
	becomes := p.Status
	if showsUserFraction(p.Status, p.UserFraction) {
		becomes += fmt.Sprintf(", userFraction %v", p.UserFraction)
	}
	rows := [][2]string{
		{"from", p.From},
		{"source", describeRelease(p.Source)},
		{"to", p.To},
		{"becomes", becomes},
		{"replaced", describeReleases(p.Replaced)},
	}
	for _, r := range rows {
		pad := strings.Repeat(" ", 11-len(r[0]))
		if _, err := fmt.Fprintf(w, line, r[0], pad, r[1]); err != nil {
			return err
		}
	}
	return nil
}

// describeRelease renders a release as "2.4.0 [versionCodes 143], halted".
func describeRelease(r tracks.Release) string {
	name := r.Name
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("%s [versionCodes %s], %s", name, strings.Join(r.VersionCodes, ", "), r.Status)
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

// replacesNote is the dry-run line saying the destination's releases go,
// without naming them: a dry run reads no track.
func replacesNote(track string) string {
	return "every release on " + track + " (unread: --plan lists them)"
}

// showsUserFraction mirrors upload's helper: inProgress always shows
// userFraction (the fraction IS the rollout percentage); completed
// only shows it when non-zero; draft / unspecified hide it.
func showsUserFraction(status string, userFraction float64) bool {
	if status == tracks.StatusInProgress {
		return true
	}
	if status == tracks.StatusCompleted && userFraction > 0 {
		return true
	}
	return false
}

func renderTable(w io.Writer, p Payload) error {
	r := p.Result
	if _, err := fmt.Fprintf(w,
		"versionCode:  %d\ntrack:        %s\nstatus:       %s\n",
		r.VersionCode, r.Track, r.Status,
	); err != nil {
		return err
	}
	if showsUserFraction(r.Status, r.UserFraction) {
		if _, err := fmt.Fprintf(w, "userFraction: %v\n", r.UserFraction); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "releaseName:  %s\n", r.ReleaseName); err != nil {
		return err
	}
	if r.DefaultLanguage != "" {
		if _, err := fmt.Fprintf(w, "defaultLang:  %s\n", r.DefaultLanguage); err != nil {
			return err
		}
	}
	if len(r.Locales) > 0 {
		if _, err := fmt.Fprintf(w, "locales:      %v\n", r.Locales); err != nil {
			return err
		}
	}
	if p.DryRun {
		if _, err := fmt.Fprintf(w, "replaces:     %s\n", replacesNote(r.Track)); err != nil {
			return err
		}
	}
	return nil
}

// dryRunJSON is the --dry-run JSON shape: the gplay Result led by the dryRun
// marker, then the ADR-0017 requires array and the statement that the
// destination's releases are replaced, which a dry run cannot list.
type dryRunJSON struct {
	DryRun bool `json:"dryRun"`
	*orchestrator.Result
	Requires            []string `json:"requires"`
	ReplacesDestination bool     `json:"replacesDestination"`
}

func renderJSON(w io.Writer, p Payload) error {
	r := p.Result
	if len(r.RawTrackResponse) > 0 {
		_, err := w.Write(r.RawTrackResponse)
		return err
	}
	if p.DryRun {
		return output.WriteJSON(w, dryRunJSON{DryRun: true, Result: r, Requires: output.NonNil(r.Requires), ReplacesDestination: true})
	}
	return output.WriteJSON(w, r)
}

func renderMarkdown(w io.Writer, p Payload) error {
	r := p.Result
	if _, err := fmt.Fprintf(w,
		"- **versionCode**: %d\n- **track**: %s\n- **status**: %s\n",
		r.VersionCode, r.Track, r.Status,
	); err != nil {
		return err
	}
	if showsUserFraction(r.Status, r.UserFraction) {
		if _, err := fmt.Fprintf(w, "- **userFraction**: %v\n", r.UserFraction); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "- **releaseName**: %s\n", r.ReleaseName); err != nil {
		return err
	}
	if r.DefaultLanguage != "" {
		if _, err := fmt.Fprintf(w, "- **defaultLang**: %s\n", r.DefaultLanguage); err != nil {
			return err
		}
	}
	if len(r.Locales) > 0 {
		if _, err := fmt.Fprintf(w, "- **locales**: %v\n", r.Locales); err != nil {
			return err
		}
	}
	if p.DryRun {
		if _, err := fmt.Fprintf(w, "- **replaces**: %s\n", replacesNote(r.Track)); err != nil {
			return err
		}
	}
	return nil
}

// Run is the business function the kernel invokes. Validates the flag
// combination, resolves the package, builds an authenticated HTTP
// client from the active Account, then hands off to the orchestrator.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if (in.ReleaseNotesSet || in.ReleaseNotes != "") && in.ReleaseNotesDir != "" {
		return nil, &exit.UsageError{Msg: "--release-notes and --release-notes-dir are mutually exclusive"}
	}
	if in.ReleaseNotesSet {
		if err := notes.CheckText(in.ReleaseNotes); err != nil {
			return nil, err
		}
	}
	state, err := orchestrator.RequestedState(in.Draft, in.Complete, in.StagedFractionSet, in.StagedFraction)
	if err != nil {
		return nil, err
	}
	if in.UpdatePrioritySet && (in.UpdatePriority < 0 || in.UpdatePriority > orchestrator.MaxUpdatePriority) {
		return nil, exit.Usagef("--update-priority must be in 0..%d, got %d", orchestrator.MaxUpdatePriority, in.UpdatePriority)
	}
	if in.FromTrack == "" {
		return nil, exit.Usagef("missing --from: pass --from <track> (the track holding the release to promote)")
	}
	if in.ToTrack == "" {
		return nil, exit.Usagef("missing --to: pass --to <track> (the destination track)")
	}
	if in.FromTrack == in.ToTrack {
		return nil, exit.Usagef("--from and --to are both %s: promote copies a release to another track (use gplay releases rollout/halt/resume/complete to change a release in place)", in.ToTrack)
	}
	if in.Plan && in.DryRun {
		return nil, exit.Usagef("--plan and --dry-run are mutually exclusive: --dry-run stays offline, --plan reads the live tracks")
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

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
		// "" keeps the implicit per-promote Edit.
		if explicitEditID, err = rc.ExplicitEditID(pkg); err != nil {
			return nil, err
		}
	}

	// A plan commits nothing, so the commit flags have nothing to apply to.
	var commit edits.CommitOptions
	if !in.Plan {
		commit = in.Commit.For(rc, explicitEditID)
	}
	result, err := orchestrator.Promote(rc.Ctx, httpClient, orchestrator.PromoteOpts{
		Package:           pkg,
		FromTrack:         in.FromTrack,
		ToTrack:           in.ToTrack,
		VersionCode:       in.VersionCode,
		ReleaseName:       in.ReleaseName,
		State:             state,
		UpdatePriority:    updatePriority(in),
		ReleaseNotes:      in.ReleaseNotes,
		ReleaseNotesDir:   in.ReleaseNotesDir,
		KeepEditOnFailure: in.KeepEditOnFailure,
		ExplicitEditID:    explicitEditID,
		Commit:            commit,
		Confirm:           in.Confirm,
		DryRun:            in.DryRun,
		Plan:              in.Plan,
	})
	if err != nil {
		// A destination tracks.update 404 means --to does not exist yet;
		// attach the `gplay tracks create <name>` hint. Every other failure
		// (and the exit code) passes through untouched.
		return nil, trackhint.Classify(in.ToTrack, err)
	}
	if in.Plan {
		rc.Notef("plan only: nothing was sent; run again without --plan to apply")
		return Payload{Result: result}, nil
	}
	// DESIGN §8: a committed mutation prints one ✓ line on stderr (never on a
	// --dry-run). userFraction is shown only when status is inProgress.
	if !in.DryRun {
		extra := ""
		if result.Status == tracks.StatusInProgress {
			extra = ", userFraction " + output.Percent(result.UserFraction)
		}
		if len(result.ReplacedVersionCodes) > 0 {
			extra += ", replaced versionCode " + joinCodes(result.ReplacedVersionCodes)
		}
		rc.ConfirmMutation(explicitEditID, "promoted versionCode %d: %s → %s (status %s%s)",
			result.VersionCode, in.FromTrack, in.ToTrack, result.Status, extra)
	}
	return Payload{Result: result, DryRun: in.DryRun}, nil
}

// NewCommand returns the cobra command for `gplay releases promote`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag        string
		in                Input
		stagedFractionVar float64
	)
	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Promote a release from one track to another (no AAB re-upload)",
		Long: `Copy the latest release on --from to --to, keeping the same versionCode.

The copy replaces every release already on --to, whatever its status (a
halted or draft release there is removed): --plan reads both tracks and
lists the releases it would replace, without changing anything; --dry-run
reads no track, so it cannot name them. --from and --to must differ.

Targeting production defaults to a draft release, which reaches no user,
unless --complete or --staged is supplied (both require --confirm there).
Release notes carry over from the source unless --release-notes /
--release-notes-dir is passed. The source's country targeting carries over
to a staged rollout (--staged); a draft or completed release cannot hold it.

When the source track has multiple coexisting releases (e.g. inProgress +
halted), pass --version-code N or --release-name <name> to pick one.`,
		Example: `  # Promote the latest beta release to production as a draft (the default)
  gplay releases promote --from beta --to production

  # Promote straight into a 5% staged rollout, with new release notes
  gplay releases promote --from beta --to production --staged 0.05 \
    --release-notes-dir distribution/whatsnew --confirm

  # Preview promoting one of two coexisting releases
  gplay releases promote --from alpha --to beta --version-code 1042 --dry-run

  # Read both tracks first: which release moves, which releases on beta it replaces
  gplay releases promote --from alpha --to beta --plan`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.StagedFractionSet = cmd.Flags().Changed("staged")
			in.StagedFraction = stagedFractionVar
			in.UpdatePrioritySet = cmd.Flags().Changed("update-priority")
			in.ReleaseNotesSet = cmd.Flags().Changed("release-notes")
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.FromTrack, "from", "", "source track to promote from (required)")
	cmd.Flags().StringVar(&in.ToTrack, "to", "", "destination track to promote to (required)")
	cmd.Flags().IntVar(&in.VersionCode, "version-code", 0, "pick the source release with this versionCode (disambiguator)")
	cmd.Flags().StringVar(&in.ReleaseName, "release-name", "", "pick the source release with this name (disambiguator)")
	cmd.Flags().StringVar(&in.ReleaseNotes, "release-notes", "", "override carry-over with this text (applied to the app's default language)")
	cmd.Flags().StringVar(&in.ReleaseNotesDir, "release-notes-dir", "", "override carry-over with per-locale files (<locale>.txt in BCP 47, e.g. en-US.txt; optional default.txt)")
	cmd.Flags().BoolVar(&in.Draft, "draft", false, "force the release status to draft on the destination")
	cmd.Flags().BoolVar(&in.Complete, "complete", false, "force the release status to completed (a full rollout, no user fraction)")
	cmd.Flags().Float64Var(&stagedFractionVar, "staged", 0, "start a staged rollout at this fraction (0 < f < 1; for 100% use --complete)")
	cmd.Flags().IntVar(&in.UpdatePriority, "update-priority", 0, "in-app update priority on the destination, 0 to 5 (default: carry over the source release's priority)")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	commitflags.Register(cmd, &in.Commit)
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "explicit confirmation required when promoting to production with --complete / --staged")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the release payload without any HTTP call")
	cmd.Flags().BoolVar(&in.Plan, "plan", false, "read both tracks and show the release this copies, the state it lands in, and the releases on --to it replaces, without changing anything (needs an Account)")
	kernel.ExperimentalFlag(cmd, "plan")
	return cmd
}

// joinCodes renders versionCodes as "140, 141".
func joinCodes(codes []int) string {
	s := make([]string, len(codes))
	for i, c := range codes {
		s[i] = strconv.Itoa(c)
	}
	return strings.Join(s, ", ")
}

// updatePriority maps the flag pair to the orchestrator's optional priority:
// nil when --update-priority was not passed, so the field stays off the wire and
// the source release's priority carries over.
func updatePriority(in Input) *int {
	if !in.UpdatePrioritySet {
		return nil
	}
	p := in.UpdatePriority
	return &p
}
