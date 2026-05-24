// Package upload implements `gplay releases upload`: the CLI glue that
// resolves --package, parses the status flags (per ADR-0002), and hands
// an Opts payload to internal/releases/orchestrator. All the actual
// HTTP and Edit-lifecycle work lives in the orchestrator and its
// internal/play/* dependencies.
package upload

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package           string
	Track             string
	AABPath           string
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

// usageError is a CLI-misuse error with ExitCode()=2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// authError signals "no account resolved"; ExitCode()=10 per
// docs/DESIGN.md §9 and the resolver precedence rules.
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
func (e *authError) ExitCode() int { return 10 }

// Payload satisfies output.Renderable for the resulting upload Result.
type Payload struct {
	Result *orchestrator.Result
}

// Renderers returns the per-Format renderers. The JSON form is API
// pass-through (the raw tracks.update response, per ADR-0003); the
// table form is a human-shaped summary.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Result) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p.Result) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Result) },
	}
}

func renderTable(w io.Writer, r *orchestrator.Result) error {
	_, err := fmt.Fprintf(w,
		"versionCode: %d\ntrack:        %s\nstatus:       %s\nuserFraction: %v\nreleaseName:  %s\n",
		r.VersionCode, r.Track, r.Status, r.UserFraction, r.ReleaseName,
	)
	if err != nil {
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
	// API pass-through: emit the raw tracks.update body (ADR-0003).
	if len(r.RawTrackResponse) > 0 {
		_, err := w.Write(r.RawTrackResponse)
		return err
	}
	// Fallback to the gplay Result shape if we somehow lost the raw.
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func renderMarkdown(w io.Writer, r *orchestrator.Result) error {
	_, err := fmt.Fprintf(w,
		"- **versionCode**: %d\n- **track**: %s\n- **status**: %s\n- **userFraction**: %v\n- **releaseName**: %s\n",
		r.VersionCode, r.Track, r.Status, r.UserFraction, r.ReleaseName,
	)
	return err
}

// Run is the business function the kernel invokes. It validates the
// flag combination, resolves the package, builds an authenticated HTTP
// client from the active Account, then hands off to the orchestrator.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	// Mutual exclusion validation (ADR-0002 / DESIGN §3 / §9).
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
	if in.AABPath == "" {
		return nil, &usageError{msg: "missing AAB path: gplay releases upload <aab> ..."}
	}

	// Resolve package: --package flag → project pin.
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

	// Dry-run skips auth entirely: nothing hits the network, so a
	// missing Account is not a problem here. The orchestrator handles
	// the dry-run path before any HTTP would happen.
	var httpClient *http.Client
	if !in.DryRun {
		if rc.Account == nil {
			return nil, &authError{msg: "no Account resolved; run gplay auth login or set GPLAY_SERVICE_ACCOUNT"}
		}
		ts, err := token.Source(rc.Ctx, rc.Account)
		if err != nil {
			return nil, &authError{msg: "could not build token source: " + err.Error()}
		}
		// oauth2.NewClient inherits ctx's oauth2.HTTPClient for the underlying
		// transport, so a test-injected RoundTripper sees both the /token
		// exchange and the androidpublisher calls.
		httpClient = oauth2.NewClient(rc.Ctx, ts)
	}

	// Translate flag-shape Status to orchestrator Status.
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

	result, err := orchestrator.Upload(rc.Ctx, httpClient, orchestrator.Opts{
		Package:           pkg,
		Track:             in.Track,
		AABPath:           in.AABPath,
		Status:            status,
		UserFraction:      in.StagedFraction,
		ReleaseNotes:      in.ReleaseNotes,
		ReleaseNotesDir:   in.ReleaseNotesDir,
		KeepEditOnFailure: in.KeepEditOnFailure,
		Confirm:           in.Confirm,
		DryRun:            in.DryRun,
	})
	if err != nil {
		return nil, err
	}
	return Payload{Result: result}, nil
}

// NewCommand returns the cobra command for `gplay releases upload`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag        string
		in                Input
		stagedFractionVar float64
	)
	cmd := &cobra.Command{
		Use:   "upload <aab>",
		Short: "Upload an AAB to a track on Google Play",
		Long: `Upload an AAB and attach it to a release on the given track.

Performs the full Edit lifecycle in one call:
  edits.insert → bundles.upload → tracks.update → edits.commit

Targeting production defaults to a draft release (ADR-0002) unless
--complete or --staged is supplied. Any string is accepted as --track
so closed-test tracks with custom names just work.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.AABPath = args[0]
			// Detect explicit --staged so 0 is distinguished from "unset".
			in.StagedFractionSet = cmd.Flags().Changed("staged")
			in.StagedFraction = stagedFractionVar
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Track, "track", "", "target track (internal, alpha, beta, production, or any closed-track name)")
	cmd.Flags().StringVar(&in.ReleaseNotes, "release-notes", "", "release notes text (applied to the app's default language)")
	cmd.Flags().StringVar(&in.ReleaseNotesDir, "release-notes-dir", "", "directory of <locale>.txt files (with optional default.txt fallback)")
	cmd.Flags().BoolVar(&in.Draft, "draft", false, "force the release status to draft")
	cmd.Flags().BoolVar(&in.Complete, "complete", false, "force the release status to completed (1.0 user fraction)")
	cmd.Flags().Float64Var(&stagedFractionVar, "staged", 0, "start a staged rollout at this fraction (0 < f ≤ 1.0)")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "explicit confirmation required for production publishes (--complete / --staged on production)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the release payload without any HTTP call")
	return cmd
}
