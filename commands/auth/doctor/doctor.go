// Package doctor implements `gplay auth doctor`: ordered diagnostic
// checks (per docs/DESIGN.md §1) that catch credential misconfiguration
// before any other command tries to talk to the API.
//
// Checks 1–3 always run. For each `--package <name>` passed, the
// per-package edits.insert/delete round-trip is appended to the chain.
package doctor

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	authdoctor "github.com/PollyGlot/google-play-cli/internal/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

// Input carries doctor-specific flags.
type Input struct {
	Packages []string
}

// Payload wraps the ordered slice of check results so the kernel can
// dispatch the right Renderer per Format.
type Payload struct {
	Results []authdoctor.CheckResult
}

// Renderers satisfies output.Renderable.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Results) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Results) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Results) },
	}
}

// failedError surfaces the worst check failure to exit.For.
type failedError struct {
	first authdoctor.CheckResult
}

func (e *failedError) Error() string {
	if e.first.Hint != "" {
		return e.first.Name + ": " + e.first.Hint
	}
	return e.first.Name + ": check failed"
}

func (e *failedError) ExitCode() int {
	if e.first.ExitCode != 0 {
		return e.first.ExitCode
	}
	return 10
}

// Run executes the check chain, renders the checklist itself (so a
// failing check still shows the diagnostic alongside the error), and
// returns nil + failedError on failure. Returning (nil, err) keeps
// kernel.Run on the default "errors → stderr only" path; the
// rendering happens here so doctor's "checklist on stdout even when
// red" contract stays local to doctor.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	base := doctorBaseHTTP(rc)
	wrapped, obs := transport.WithScopeObserver(base.Transport)
	// Clone the base client so injected Timeout/CheckRedirect/Jar
	// survive: only the Transport is replaced.
	hc := *base
	hc.Transport = wrapped

	checks := buildChecks(obs, in.Packages)

	var results []authdoctor.CheckResult
	var worst *authdoctor.CheckResult
	// doctor's whole job is to exercise auth, so resolve the credential
	// now (this is where the keyring probe fires for a stored Account).
	// EnsureAccount splits absent from invalid (ADR-0020, DESIGN §1): an
	// invalid credential returns the real resolution error, while an
	// absent one returns nil + nil Account. Surface the real cause in
	// check 1 when resolution actually failed; keep the generic synthetic
	// message only for the genuinely-absent case.
	if err := rc.EnsureAccount(); err != nil {
		results, worst = synthFailure(err, checks)
	} else if rc.Account == nil {
		results, worst = synthFailure(errors.New("no active account; run `gplay auth login`"), checks)
	} else {
		results = authdoctor.Run(rc.Ctx, rc.Account, &hc, checks...)
		worst = worstFailure(results)
	}

	payload := Payload{Results: results}
	// Render the checklist regardless of pass/fail so the user always
	// sees an ordered list, then return failedError so exit.For picks
	// up the worst check's code.
	if err := output.Render(rc.Stdout, rc.Format, payload.Renderers()); err != nil {
		return nil, err
	}
	if worst != nil {
		return nil, &failedError{first: *worst}
	}
	return nil, nil
}

// NewCommand returns the cobra command for `gplay auth doctor`.
func NewCommand(boot kernel.Boot) *cobra.Command {
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
4. Token can be minted for the playdeveloperreporting scope (vitals)
5. (Per --package) edits.insert + edits.delete round-trip on the package

Checks 1–4 run once. Check 5 runs once per --package value passed (in
order). Checks run in order and the chain stops on the first failure;
subsequent checks are reported as skipped. Use --output json to get a
structured []CheckResult for scripting.`,
		// A failing doctor is a normal exit path, not a usage error;
		// silence cobra's usage banner so it does not collide with the
		// rendered checklist on stdout.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{Packages: packages})
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringSliceVar(&packages, "package", nil,
		"Android package name to round-trip an edits.insert+delete against; repeat for multiple packages")
	return cmd
}

// doctorBaseHTTP returns the underlying http.Client doctor wraps with
// its scope observer. Tests inject a base via ctx.Value(oauth2.HTTPClient);
// production falls back to http.DefaultClient (each check minted its
// own oauth2 client on top via oauth2.NewClient(ctx, ts)).
func doctorBaseHTTP(rc *kernel.RunContext) *http.Client {
	if v := rc.Ctx.Value(oauth2.HTTPClient); v != nil {
		if c, ok := v.(*http.Client); ok && c != nil {
			return c
		}
	}
	return &http.Client{}
}

func buildChecks(obs *transport.ScopeObserver, packages []string) []authdoctor.Check {
	checks := authdoctor.DefaultChecks(obs)
	for _, p := range packages {
		checks = append(checks, authdoctor.CheckPackageAccess(p))
	}
	return checks
}

// worstFailure returns the highest-exit-code failing check; ties break
// on order.
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

// synthFailure builds check #1 as failed + the rest as skipped when
// resolution itself died (no active account).
func synthFailure(err error, checks []authdoctor.Check) ([]authdoctor.CheckResult, *authdoctor.CheckResult) {
	failure := authdoctor.ResolutionFailure(err)[0]
	results := make([]authdoctor.CheckResult, 0, len(checks))
	results = append(results, failure)
	for i := 1; i < len(checks); i++ {
		results = append(results, authdoctor.NewSkippedResult(checks[i]))
	}
	return results, &results[0]
}

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

func renderTable(w io.Writer, results []authdoctor.CheckResult) error {
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
}

func renderMarkdown(w io.Writer, results []authdoctor.CheckResult) error {
	for _, r := range results {
		var line string
		switch {
		case r.Skipped:
			line = fmt.Sprintf("- [ ] _skipped_: %s\n", r.Name)
		case !r.Passed:
			if r.Hint != "" {
				line = fmt.Sprintf("- [ ] %s (hint: %s)\n", r.Name, r.Hint)
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
