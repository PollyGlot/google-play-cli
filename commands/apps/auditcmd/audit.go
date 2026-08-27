// Package auditcmd implements `gplay apps audit` (PRD #449): a strictly
// read-only consistency sweep over a set of apps, emitting a findings report
// and exiting 70 when anything was found, so CI can gate on "is my account in
// a clean state?" without clicking through the Play Console app by app.
//
// Why `apps audit` and not `account audit`: CONTEXT.md reserves **Account**
// for a locally registered credential and warns against writing "account"
// unqualified (three different things end in "account" here). The sweep's unit
// is the app, so the command hangs off the `apps` noun with a
// reference/diagnostic verb, the ADR-0019 category that already holds `doctor`
// and `validate`.
//
// Composition, not a new surface: discovery rides `apps.search` (ADR-0039, the
// same read as `apps accessible list`) and each app is read through the
// existing read-only Edit path (edits.tracks.list + edits.listings.list). No
// new API method, no mutation: the only non-GET calls in the whole sweep are
// the open/discard of the throwaway Edit each read needs, which is how every
// gplay read works (edits.WithReadOnlyEdit never commits).
//
// The report is a gplay-owned document, not an API mirror: it composes several
// resources, so ADR-0003's verbatim rule has nothing to mirror. That, plus a
// check set that may still grow, is why the command ships [experimental].
//
// Quota is the real cost of a sweep (one Edit + two GETs per app), which is
// what the report's `ran` section is for: it names the apps and checks that
// actually ran, so a partial sweep is never mistaken for a clean bill.
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
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/accessibleapps"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	// Packages scopes the sweep to explicit packages, taken from the
	// positional arguments. Empty means "discover the accessible apps", which
	// costs a Reporting call and can be a large set: a user who manages three
	// apps out of an org's hundreds names them and never pays for the rest.
	//
	// Positional and variadic, like `apps add <pkg>...`, rather than a
	// repeated --package: everywhere else in gplay --package addresses ONE
	// app, and quietly giving the same flag last-wins-or-accumulate semantics
	// depending on the command is exactly the ambiguity an agent trips on.
	Packages []string
	// Checks / SkipChecks select the check set; both are validated against
	// the frozen ID registry before any network call.
	Checks     []string
	SkipChecks []string
}

// SweepError is one app the sweep could not read. It is reported inside the
// document rather than aborting the run: an audit over forty apps that dies on
// the one the credential cannot see is useless, and the whole point of the
// `ran` section is to make the hole visible.
type SweepError struct {
	Package  string `json:"package"`
	Message  string `json:"message"`
	ExitCode int    `json:"exitCode"`
}

// Ran is the what-ran section: the apps and checks that actually executed.
// Without it, "no findings" is ambiguous between a clean account and a sweep
// that silently covered nothing.
type Ran struct {
	Apps   []string `json:"apps"`
	Checks []string `json:"checks"`
}

// Summary is the counts a human reads first and a CI job can assert on.
type Summary struct {
	AppsAudited int `json:"appsAudited"`
	AppsFailed  int `json:"appsFailed"`
	Findings    int `json:"findings"`
}

// Report is the gplay-owned findings document: the JSON contract of the
// command (experimental, so free to move until the check set settles).
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

// Payload satisfies output.Renderable. The JSON form is the whole Report (a
// gplay document, not an API pass-through); the table and markdown forms show
// the findings and append the what-ran / errors context, which a bare table
// cannot carry.
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

// renderText draws the findings with the shared table machinery, then the
// what-ran and per-app-failure context. A findings table alone would let a
// reader mistake an empty sweep for a clean one.
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

// Run performs the sweep: resolve the app set, read one snapshot per app, run
// the selected checks, build the report. It renders nothing and mutates
// nothing; the caller (RunE) decides the exit code from the report.
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

	var (
		snapshots []audit.Snapshot
		ran       []string
		failures  []SweepError
	)
	for _, pkg := range packages {
		snap, readErr := readSnapshot(rc, httpClient, pkg)
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

	// The cross-app context is built from the successful snapshots only:
	// guessing at an app the sweep could not read would turn an API failure
	// into false locale-drift findings on its peers.
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

// resolvePackages returns the app set to sweep, deduplicated and ordered.
// Named packages win (explicit scope, no Reporting call, no quota spent on apps
// the operator does not manage); otherwise the set is discovered from
// apps.search, the same server-authoritative inventory `apps accessible list`
// prints (ADR-0039).
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

// discoverPackages pages through apps.search and returns every accessible
// package name, sorted for a deterministic sweep order.
//
// It mints its OWN reporting-scoped client rather than annotating the command
// with kernel.WithScope: the sweep needs BOTH scopes (Reporting to discover,
// androidpublisher to read each app), and a command carries one. Doing it here
// keeps least privilege where it matters: a package-scoped run never asks
// for a Reporting token at all.
func discoverPackages(rc *kernel.RunContext) ([]string, error) {
	reporting := *rc
	reporting.Scope = token.ReportingScope
	hc, err := reporting.AuthedClient()
	if err != nil {
		return nil, err
	}

	var (
		pkgs      []string
		pageToken string
	)
	// Bounded: a runaway or looping nextPageToken must not turn an audit into
	// an unbounded quota burn. 1000 pages is far past any real account.
	for page := 0; page < 1000; page++ {
		sr, _, err := accessibleapps.Search(rc.Ctx, hc, 0, pageToken)
		if err != nil {
			return nil, &discoveryError{cause: err}
		}
		for _, a := range sr.Apps {
			if a.PackageName != "" {
				pkgs = append(pkgs, a.PackageName)
			}
		}
		if sr.NextPageToken == "" {
			break
		}
		pageToken = sr.NextPageToken
	}
	pkgs = dedupe(pkgs)
	sort.Strings(pkgs)
	return pkgs, nil
}

// discoveryError points at the named-packages escape hatch when discovery fails.
// A credential can hold androidpublisher rights without the Reporting access
// apps.search needs (ADR-0039), and that operator can still audit: they just
// have to name the apps. It carries no ExitCode of its own so the wrapped
// *api.Error stays authoritative.
type discoveryError struct{ cause error }

func (e *discoveryError) Error() string {
	return "apps audit: could not discover accessible apps: name the packages as arguments to audit them without the Play Developer Reporting scope: " + e.cause.Error()
}

func (e *discoveryError) Unwrap() error { return e.cause }

// readSnapshot reads one app: open a read-only Edit, list its tracks and its
// Listings, discard the Edit. Two GETs per app; the Edit is never committed.
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

// dedupe keeps first-seen order and drops blanks: `apps audit a a`
// must not audit `a` twice (and pay twice the quota).
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

// Emit turns a finished sweep into what the kernel needs: a Renderable on the
// clean path, and (nil, exit-70) on the findings path, having rendered the
// report to stdout itself.
//
// The asymmetry is the kernel's contract, not a quirk here: kernel.Run only
// renders on the success path, so a command that must exit non-zero WITH data
// on stdout renders it before returning the error (the `auth doctor` pattern).
// Split out of RunE so the exit-code decision is testable without driving
// cobra and a keystore.
func Emit(rc *kernel.RunContext, report Report) (output.Renderable, error) {
	payload := Payload{Report: report}
	if report.Summary.Findings == 0 {
		return payload, nil
	}
	if err := output.Render(rc.Stdout, rc.Format, payload.Renderers()); err != nil {
		return nil, err
	}
	return nil, exit.Findingsf("apps audit: %d finding(s) across %d app(s); see the report on stdout",
		report.Summary.Findings, report.Summary.AppsAudited)
}

// NewCommand returns the cobra command for `gplay apps audit`.
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
prints, ADR-0039). Name one or more packages to audit those only, which also
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

Exit 0 when clean, 70 when the report carries findings (a gate, not an error),
and the usual taxonomy when the sweep itself could not run.`,
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
