package auditcmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/audit"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/fanout"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/accessibleapps"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

type Input struct {
	Packages   []string
	Checks     []string
	SkipChecks []string
}

type SweepError struct {
	Package  string `json:"package"`
	Message  string `json:"message"`
	ExitCode int    `json:"exitCode"`
}

type Ran struct {
	Apps   []string `json:"apps"`
	Checks []string `json:"checks"`
}

type Summary struct {
	AppsAudited int `json:"appsAudited"`
	AppsFailed  int `json:"appsFailed"`
	Findings    int `json:"findings"`
}

type Report struct {
	Ran      Ran             `json:"ran"`
	Findings []audit.Finding `json:"findings"`
	Errors   []SweepError    `json:"errors,omitempty"`
	Summary  Summary         `json:"summary"`
}

var columns = []output.Column[audit.Finding]{
	{Key: "package", Header: "PACKAGE", Value: func(f audit.Finding) string { return f.Package }},
	{Key: "check", Header: "CHECK", Value: func(f audit.Finding) string { return f.Check }},
	{Key: "severity", Header: "SEVERITY", Value: func(f audit.Finding) string { return string(f.Severity) }},
	{Key: "detail", Header: "DETAIL", Value: func(f audit.Finding) string { return f.Message }},
}

type Payload struct {
	Report Report
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderText(w, output.RenderTable[audit.Finding]) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Report) },
		Markdown: func(w io.Writer) error { return p.renderText(w, output.RenderMarkdown[audit.Finding]) },
	}
}

func (p Payload) renderText(w io.Writer, draw func(io.Writer, []output.Column[audit.Finding], []audit.Finding) error) error {
	if len(p.Report.Findings) > 0 {
		if err := draw(w, columns, p.Report.Findings); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "audited %d app(s) with %d check(s): %d finding(s)\n",
		p.Report.Summary.AppsAudited, len(p.Report.Ran.Checks), p.Report.Summary.Findings); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "checks: %s\n", strings.Join(p.Report.Ran.Checks, ", ")); err != nil {
		return err
	}
	for _, e := range p.Report.Errors {
		if _, err := fmt.Fprintf(w, "not audited: %s (%s)\n", e.Package, e.Message); err != nil {
			return err
		}
	}
	return nil
}

func Run(rc *kernel.RunContext, in Input) (Report, error) {
	checks, err := audit.Select(in.Checks, in.SkipChecks)
	if err != nil {
		var unknown *audit.UnknownCheckError
		if errors.As(err, &unknown) {
			return Report{}, exit.Usagef("apps audit: %s", err.Error())
		}
		return Report{}, err
	}
	if len(checks) == 0 {
		return Report{}, exit.Usagef("apps audit: no checks selected: --skip-check excluded every check")
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return Report{}, err
	}

	packages, err := resolvePackages(rc, in)
	if err != nil {
		return Report{}, err
	}

	snaps := make([]audit.Snapshot, len(packages))
	readErrs := make([]error, len(packages))
	_ = fanout.Each(len(packages), func(i int) error {
		snaps[i], readErrs[i] = readSnapshot(rc, httpClient, packages[i])
		return nil
	})

	var (
		snapshots []audit.Snapshot
		ran       []string
		failures  []SweepError
	)
	for i, pkg := range packages {
		snap, readErr := snaps[i], readErrs[i]
		if readErr != nil {
			failures = append(failures, SweepError{
				Package:  pkg,
				Message:  readErr.Error(),
				ExitCode: exit.For(readErr),
			})
			continue
		}
		snapshots = append(snapshots, snap)
		ran = append(ran, pkg)
	}

	sweep := audit.BuildContext(snapshots)
	findings := make([]audit.Finding, 0)
	for _, s := range snapshots {
		findings = append(findings, audit.Evaluate(s, checks, sweep)...)
	}

	checkIDs := make([]string, 0, len(checks))
	for _, c := range checks {
		checkIDs = append(checkIDs, c.ID)
	}
	return Report{
		Ran:      Ran{Apps: ran, Checks: checkIDs},
		Findings: findings,
		Errors:   failures,
		Summary: Summary{
			AppsAudited: len(ran),
			AppsFailed:  len(failures),
			Findings:    len(findings),
		},
	}, nil
}

func resolvePackages(rc *kernel.RunContext, in Input) ([]string, error) {
	if len(in.Packages) > 0 {
		return dedupe(in.Packages), nil
	}
	pkgs, err := discoverPackages(rc)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, exit.Usagef("apps audit: no accessible apps to audit: name the packages as arguments to audit them explicitly")
	}
	return pkgs, nil
}

func discoverPackages(rc *kernel.RunContext) ([]string, error) {
	reporting := *rc
	reporting.Scope = token.ReportingScope
	hc, err := reporting.AuthedClient()
	if err != nil {
		return nil, err
	}

	pkgs, _, err := api.Paginate(api.Pager{Op: "apps.search", What: "apps.search", MaxPages: maxDiscoveryPages},
		func(token string, _ int) ([]string, string, error) {
			sr, _, err := accessibleapps.Search(rc.Ctx, hc, 0, token)
			if err != nil {
				return nil, "", err
			}
			page := make([]string, 0, len(sr.Apps))
			for _, a := range sr.Apps {
				if a.PackageName != "" {
					page = append(page, a.PackageName)
				}
			}
			return page, sr.NextPageToken, nil
		})
	if err != nil {
		return nil, &discoveryError{cause: err}
	}
	pkgs = dedupe(pkgs)
	sort.Strings(pkgs)
	return pkgs, nil
}

const maxDiscoveryPages = 1000

type discoveryError struct{ cause error }

func (e *discoveryError) Error() string {
	return "apps audit: could not discover accessible apps: name the packages as arguments to audit them without the Play Developer Reporting scope: " + e.cause.Error()
}

func (e *discoveryError) Unwrap() error { return e.cause }

func readSnapshot(rc *kernel.RunContext, hc *http.Client, pkg string) (audit.Snapshot, error) {
	snap := audit.Snapshot{Package: pkg}
	err := edits.WithReadOnlyEdit(rc.Ctx, hc, pkg, func(editID string) error {
		ts, _, err := tracks.List(rc.Ctx, hc, pkg, editID)
		if err != nil {
			return err
		}
		ls, _, err := listings.List(rc.Ctx, hc, pkg, editID)
		if err != nil {
			return err
		}
		snap.Tracks, snap.Listings = ts, ls
		return nil
	})
	if err != nil {
		return audit.Snapshot{}, err
	}
	return snap, nil
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

var retryableExitCodes = map[int]struct{}{40: {}, 50: {}}

type sweepFailedError struct {
	errs      []SweepError
	audited   int
	findings  int
	firstCode int
}

func newSweepFailedError(r Report) *sweepFailedError {
	e := &sweepFailedError{errs: r.Errors, audited: r.Summary.AppsAudited, findings: r.Summary.Findings}
	firstRetryable := 0
	for _, se := range r.Errors {
		if _, ok := retryableExitCodes[se.ExitCode]; ok {
			if firstRetryable == 0 {
				firstRetryable = se.ExitCode
			}
			continue
		}
		if se.ExitCode != 0 {
			e.firstCode = se.ExitCode
			return e
		}
	}
	if firstRetryable != 0 {
		e.firstCode = firstRetryable
	}
	return e
}

func (e *sweepFailedError) ExitCode() int {
	if e.firstCode != 0 {
		return e.firstCode
	}
	return 1
}

func (e *sweepFailedError) Error() string {
	failed := make([]string, 0, len(e.errs))
	for _, se := range e.errs {
		failed = append(failed, se.Package)
	}
	return fmt.Sprintf("apps audit: %d app(s) could not be read (%s): %d app(s) audited, %d finding(s); the report covers only what ran",
		len(failed), strings.Join(failed, ", "), e.audited, e.findings)
}

func Emit(rc *kernel.RunContext, report Report) (output.Renderable, error) {
	payload := Payload{Report: report}
	if len(report.Errors) == 0 && report.Summary.Findings == 0 {
		return payload, nil
	}
	if err := output.Render(rc.Stdout, rc.Format, payload.Renderers()); err != nil {
		return nil, err
	}
	if len(report.Errors) > 0 {
		return nil, newSweepFailedError(report)
	}
	return nil, exit.Findingsf("apps audit: %d finding(s) across %d app(s); see the report on stdout",
		report.Summary.Findings, report.Summary.AppsAudited)
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "audit [package...]",
		Short: "Sweep apps for consistency drift (read-only)",
		Long: `Sweep apps for consistency drift and report what it finds. Strictly
read-only: it opens no Edit it does not immediately discard, and writes
nothing.

Scope: with no arguments, the sweep covers every app the credential can see
(the same server-authoritative inventory ` + "`gplay apps accessible list`" + `
prints). Name one or more packages to audit those only, which also
skips the Play Developer Reporting call discovery needs. Each audited app
costs one throwaway Edit and two reads, so scope a large account deliberately.

Checks (IDs are stable, usable as CI filters):

  lingering-drafts       a track still holds a draft release
  locale-drift           the app's Listing locales are a subset of the set
                         seen across the audited apps (needs 2+ apps)
  empty-release-notes    a shipped release has no (or blank) release notes
  no-production-release  the production track carries no release

Select with --check (repeatable, defaults to all) and --skip-check. The report
names the apps and checks that actually ran, so a partial sweep is never read
as a clean bill; an app the sweep could not read is listed under errors and
does not abort the run.

Exit 0 when every app was read and nothing was found, 70 when the report
carries findings (a gate, not an error), and the ordinary API/network code
whenever an app could not be read at all: a sweep with holes never reports
clean.`,
		Example: `  # Sweep every app the credential can see
  gplay apps audit

  # Gate CI on draft releases left behind on two apps (exit 70 on findings)
  gplay apps audit com.example.app com.example.lite --check lingering-drafts

  gplay apps audit --skip-check locale-drift --output json`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.Packages = args
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				report, err := Run(rc, in)
				if err != nil {
					return nil, err
				}
				return Emit(rc, report)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringArrayVar(&in.Checks, "check", nil, "check to run (repeatable; default: all of "+strings.Join(audit.IDs(), ", ")+")")
	cmd.Flags().StringArrayVar(&in.SkipChecks, "skip-check", nil, "check to skip (repeatable; applied after --check)")
	return cmd
}
