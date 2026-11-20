package doctor

import (
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	authdoctor "github.com/PollyGlot/google-play-cli/internal/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/redact"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

type Input struct {
	Packages []string
}

type Payload struct {
	Results []authdoctor.CheckResult
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Results) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Results) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Results) },
	}
}

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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	base := doctorBaseHTTP(rc)
	wrapped, obs := transport.WithScopeObserver(base.Transport)
	hc := *base
	hc.Transport = wrapped

	checks := buildChecks(obs, in.Packages)

	var results []authdoctor.CheckResult
	var worst *authdoctor.CheckResult
	if err := rc.EnsureAccount(); err != nil {
		results, worst = synthFailure(err, checks)
	} else if rc.Account == nil {
		results, worst = synthFailure(kernel.NoAccountError(), checks)
	} else {
		results = authdoctor.Run(rc.Ctx, rc.Account, &hc, checks...)
		worst = worstFailure(results)
	}
	if rc.KeystoreLabel == keystore.BackendKeyring {
		keystore.WarnStrayFiles(rc.Ctx, rc.Stderr, rc.KeystoreRoot)
	}

	for i := range results {
		results[i].Hint = redact.String(results[i].Hint)
	}

	payload := Payload{Results: results}
	if err := output.Render(rc.Stdout, rc.Format, payload.Renderers()); err != nil {
		return nil, err
	}
	if worst != nil {
		return nil, &failedError{first: *worst}
	}
	return nil, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		packages   []string
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run ordered diagnostic checks on the active credential",
		Long: `Run the credential checks in order
(https://gplay.sh/docs/concepts/authentication/):

1. Service account JSON is valid
2. OAuth2 access token can be minted
3. Token carries the androidpublisher scope
4. Token can be minted for the playdeveloperreporting scope (vitals)
5. (Per --package) edits.insert + edits.delete round-trip on the package

Checks 1–4 run once. Check 5 runs once per --package value passed (in
order). Checks run in order and the chain stops on the first failure;
subsequent checks are reported as skipped. Use --output json to get a
structured []CheckResult for scripting.`,
		Example: `  # Check the active credential, stopping at the first failing step
  gplay auth doctor

  # Also prove write access to two apps
  gplay auth doctor --package com.example.app --package com.example.lite

  # Diagnose a key file before registering it, as JSON for a script
  gplay auth doctor --service-account ./play-sa.json --output json`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
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

func doctorBaseHTTP(rc *kernel.RunContext) *http.Client {
	if v := rc.Ctx.Value(oauth2.HTTPClient); v != nil {
		if c, ok := v.(*http.Client); ok && c != nil {
			return c
		}
	}
	return http.DefaultClient
}

func buildChecks(obs *transport.ScopeObserver, packages []string) []authdoctor.Check {
	checks := authdoctor.DefaultChecks(obs)
	for _, p := range packages {
		checks = append(checks, authdoctor.CheckPackageAccess(p))
	}
	return checks
}

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
