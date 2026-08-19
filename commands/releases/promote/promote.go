// Package promote implements `gplay releases promote`: the CLI glue
// that parses --from/--to plus the status overrides (per ADR-0002),
// resolves --package, and hands a PromoteOpts payload to
// internal/releases/orchestrator. All the HTTP / Edit-lifecycle work
// lives in the orchestrator and its internal/play/* dependencies.
package promote

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/trackhint"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
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
	ReleaseNotesDir   string
	Draft             bool
	Complete          bool
	StagedFraction    float64
	StagedFractionSet bool
	KeepEditOnFailure bool
	Confirm           bool
	DryRun            bool
}

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Payload satisfies output.Renderable for the resulting promote Result.
// Reuses the orchestrator's Result so JSON pass-through (ADR-0003) and
// the table renderer match upload's shape.
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

// showsUserFraction mirrors upload's helper: inProgress always shows
// userFraction (the fraction IS the rollout percentage); completed
// only shows it when non-zero; draft / unspecified hide it.
func showsUserFraction(status string, userFraction float64) bool {
	if status == "inProgress" {
		return true
	}
	if status == "completed" && userFraction > 0 {
		return true
	}
	return false
}

func renderTable(w io.Writer, r *orchestrator.Result) error {
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
	return nil
}

func renderJSON(w io.Writer, r *orchestrator.Result) error {
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
	return nil
}

// Run is the business function the kernel invokes. Validates the flag
// combination, resolves the package, builds an authenticated HTTP
// client from the active Account, then hands off to the orchestrator.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.ReleaseNotes != "" && in.ReleaseNotesDir != "" {
		return nil, &usageError{msg: "--release-notes and --release-notes-dir are mutually exclusive"}
	}
	statusFlags := 0
	if in.Draft {
		statusFlags++
	}
	if in.Complete {
		statusFlags++
	}
	if in.StagedFractionSet {
		statusFlags++
	}
	if statusFlags > 1 {
		return nil, &usageError{msg: "--draft, --complete, and --staged are mutually exclusive"}
	}
	if in.StagedFractionSet && (in.StagedFraction <= 0 || in.StagedFraction > 1.0) {
		return nil, &usageError{msg: "--staged fraction must be in (0, 1]"}
	}
	if in.FromTrack == "" {
		return nil, &usageError{msg: "missing --from"}
	}
	if in.ToTrack == "" {
		return nil, &usageError{msg: "missing --to"}
	}

	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
	}

	// Dry-run skips auth entirely: nothing hits the network, so a
	// missing Account is not a problem here. The orchestrator handles
	// the dry-run path before any HTTP would happen.
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

	var status orchestrator.Status
	switch {
	case in.Draft:
		status = orchestrator.StatusDraft
	case in.Complete:
		status = orchestrator.StatusCompleted
	case in.StagedFractionSet:
		status = orchestrator.StatusInProgress
	default:
		status = orchestrator.StatusUnspecified
	}

	result, err := orchestrator.Promote(rc.Ctx, httpClient, orchestrator.PromoteOpts{
		Package:           pkg,
		FromTrack:         in.FromTrack,
		ToTrack:           in.ToTrack,
		VersionCode:       in.VersionCode,
		ReleaseName:       in.ReleaseName,
		Status:            status,
		UserFraction:      in.StagedFraction,
		ReleaseNotes:      in.ReleaseNotes,
		ReleaseNotesDir:   in.ReleaseNotesDir,
		KeepEditOnFailure: in.KeepEditOnFailure,
		ExplicitEditID:    explicitEditID,
		Confirm:           in.Confirm,
		DryRun:            in.DryRun,
	})
	if err != nil {
		// A destination tracks.update 404 means --to does not exist yet;
		// attach the `gplay tracks create <name>` hint. Every other failure
		// (and the exit code) passes through untouched.
		return nil, trackhint.Classify(in.ToTrack, err)
	}
	// DESIGN §8: a committed mutation prints one ✓ line on stderr (never on a
	// --dry-run). userFraction is shown only when status is inProgress.
	if !in.DryRun {
		extra := ""
		if result.Status == "inProgress" {
			extra = ", userFraction " + output.Percent(result.UserFraction)
		}
		rc.ConfirmMutation(explicitEditID, "promoted versionCode %d: %s → %s (status %s%s)",
			result.VersionCode, in.FromTrack, in.ToTrack, result.Status, extra)
	}
	return Payload{Result: result}, nil
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

Targeting production defaults to a draft release (ADR-0002) unless --complete
or --staged is supplied. Release notes carry over from the source unless
--release-notes / --release-notes-dir is passed.

When the source track has multiple coexisting releases (e.g. inProgress +
halted), pass --version-code N or --release-name <name> to pick one.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.StagedFractionSet = cmd.Flags().Changed("staged")
			in.StagedFraction = stagedFractionVar
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.FromTrack, "from", "", "source track to promote from")
	cmd.Flags().StringVar(&in.ToTrack, "to", "", "destination track to promote to")
	cmd.Flags().IntVar(&in.VersionCode, "version-code", 0, "pick the source release with this versionCode (disambiguator)")
	cmd.Flags().StringVar(&in.ReleaseName, "release-name", "", "pick the source release with this name (disambiguator)")
	cmd.Flags().StringVar(&in.ReleaseNotes, "release-notes", "", "override carry-over with this text (applied to the app's default language)")
	cmd.Flags().StringVar(&in.ReleaseNotesDir, "release-notes-dir", "", "override carry-over with per-locale files (<locale>.txt, optional default.txt)")
	cmd.Flags().BoolVar(&in.Draft, "draft", false, "force the release status to draft on the destination")
	cmd.Flags().BoolVar(&in.Complete, "complete", false, "force the release status to completed (1.0 user fraction)")
	cmd.Flags().Float64Var(&stagedFractionVar, "staged", 0, "start a staged rollout at this fraction (0 < f ≤ 1.0)")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "explicit confirmation required when promoting to production with --complete / --staged")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the release payload without any HTTP call")
	return cmd
}
