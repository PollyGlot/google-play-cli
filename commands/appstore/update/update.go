// Package update implements `gplay appstore update --store-package <sp>
// [--package <pkg>] --file <submission.json> --confirm`: assemble a hosted
// app's details — developer identity, active APK sets, per-locale store
// listings, policy questionnaire answers — and SUBMIT it to Google's review,
// via `appstoreappsreview.updateAppStoreHostedApp`.
//
// The input is declarative and file-only: the body mirrors
// UpdateAppStoreHostedAppRequest. There is deliberately no flag set for it —
// the policy declarations are a seven-way oneof Google keeps growing, and are
// simply not expressible as flags (see internal/play/appstore/update.go). One
// file, versionable next to the app, is also what a CI job wants.
//
// The call submits the app to review IMMEDIATELY: no staging step, no recall.
// That puts it in the destructive tier (ADR-0017): --confirm is mandatory
// (missing → exit 3 naming the flag), and --dry-run rehearses the whole thing
// — read, parse, resolve, count — with zero HTTP, reporting the gate in the
// machine-readable `requires` array. MarkMutating gates it under
// GPLAY_READONLY (exit 4). Edit-free: the call is not under `/edits/`.
package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

// Input is the request-shaped struct cobra builds from the flags.
type Input struct {
	StorePackage string
	Package      string
	File         string // "" or "-" → stdin
	Confirm      bool
	DryRun       bool
}

// summary is what a human needs to recognise the submission they are about to
// make (or just made), derived from the parsed body rather than from the
// response — UpdateAppStoreHostedAppResponse carries no fields.
type summary struct {
	DeveloperName      string
	Locales            int
	ApkSets            int
	PolicyDeclarations int
}

func summarize(req appstore.UpdateHostedAppRequest) summary {
	s := summary{
		Locales:            len(req.ActiveLocalizedStoreListings),
		PolicyDeclarations: len(req.PolicyDeclarations),
	}
	if req.AppDetails != nil {
		s.DeveloperName = req.AppDetails.DeveloperName
	}
	if req.ActiveApks != nil {
		s.ApkSets = len(req.ActiveApks.ActiveApkSets)
	}
	return s
}

// Payload renders the submitted hosted app (live) or the rehearsal (--dry-run).
// Raw carries the verbatim API body for the ADR-0003 --output json
// pass-through; the documented response models no fields, so the human views
// describe the submission from the parsed request instead.
type Payload struct {
	StorePackage string
	Package      string
	Sum          summary
	DryRun       bool
	Raw          json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

// rows is the recap shared by the human views, in a fixed order.
func (p Payload) rows() [][2]string {
	return [][2]string{
		{"APP_STORE_PACKAGE_NAME", p.StorePackage},
		{"PACKAGE_NAME", p.Package},
		{"DEVELOPER_NAME", p.Sum.DeveloperName},
		{"LOCALES", fmt.Sprintf("%d", p.Sum.Locales)},
		{"APK_SETS", fmt.Sprintf("%d", p.Sum.ApkSets)},
		{"POLICY_DECLARATIONS", fmt.Sprintf("%d", p.Sum.PolicyDeclarations)},
	}
}

func (p Payload) renderTable(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w,
			"would submit hosted app %s in app store %s to Google review: %d locale(s), %d APK set(s), %d policy declaration(s) (dry-run); requires --confirm\n",
			p.Package, p.StorePackage, p.Sum.Locales, p.Sum.ApkSets, p.Sum.PolicyDeclarations)
		return err
	}
	for _, row := range p.rows() {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\nsubmitted to Google review — the submission is immediate and cannot be recalled\n")
	return err
}

func (p Payload) renderMarkdown(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w,
			"- **dry-run**: would submit hosted app `%s` in app store `%s` to Google review (%d locale(s), %d APK set(s), %d policy declaration(s)) — requires --confirm\n",
			p.Package, p.StorePackage, p.Sum.Locales, p.Sum.ApkSets, p.Sum.PolicyDeclarations)
		return err
	}
	if _, err := fmt.Fprintf(w, "## appstore update\n\n"); err != nil {
		return err
	}
	rows := make([][]string, 0, 6)
	for _, r := range p.rows() {
		rows = append(rows, []string{r[0], r[1]})
	}
	if err := output.MarkdownTable(w, []string{"FIELD", "VALUE"}, rows); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\nSubmitted to Google review — the submission is immediate and cannot be recalled.\n")
	return err
}

// dryRunView is the gplay-shaped --dry-run JSON: the resolved target, what the
// body adds up to, and the machine-readable `requires` array (ADR-0017 §4) so
// an agent learns the gate before running live.
type dryRunView struct {
	DryRun              bool     `json:"dryRun"`
	AppStorePackageName string   `json:"appStorePackageName"`
	PackageName         string   `json:"packageName"`
	DeveloperName       string   `json:"developerName,omitempty"`
	Locales             int      `json:"locales"`
	ApkSets             int      `json:"apkSets"`
	PolicyDeclarations  int      `json:"policyDeclarations"`
	Requires            []string `json:"requires"`
}

// submittedView is the gplay-shaped success object emitted only when the API
// returns no body at all (see renderJSON).
type submittedView struct {
	OK                  bool   `json:"ok"`
	AppStorePackageName string `json:"appStorePackageName"`
	PackageName         string `json:"packageName"`
	ReviewSubmitted     bool   `json:"reviewSubmitted"`
	DeveloperName       string `json:"developerName,omitempty"`
	Locales             int    `json:"locales"`
	ApkSets             int    `json:"apkSets"`
	PolicyDeclarations  int    `json:"policyDeclarations"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{
			DryRun:              true,
			AppStorePackageName: p.StorePackage,
			PackageName:         p.Package,
			DeveloperName:       p.Sum.DeveloperName,
			Locales:             p.Sum.Locales,
			ApkSets:             p.Sum.ApkSets,
			PolicyDeclarations:  p.Sum.PolicyDeclarations,
			Requires:            []string{"confirm"},
		})
	}
	// ADR-0003: the API response goes through verbatim. The documented
	// exception applies only when there is nothing to pass through —
	// UpdateAppStoreHostedAppResponse models no fields, so a server answering
	// with an empty body (rather than `{}`) would leave --output json, the CI
	// default, with zero bytes to parse. As in `appstore create`, a
	// gplay-shaped success object stands in.
	if len(bytes.TrimSpace(p.Raw)) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, submittedView{
		OK:                  true,
		AppStorePackageName: p.StorePackage,
		PackageName:         p.Package,
		ReviewSubmitted:     true,
		DeveloperName:       p.Sum.DeveloperName,
		Locales:             p.Sum.Locales,
		ApkSets:             p.Sum.ApkSets,
		PolicyDeclarations:  p.Sum.PolicyDeclarations,
	})
}

// readBody reads the submission body from --file (or stdin when "" / "-"). A
// body that cannot be read is CLI misuse (exit 2), named precisely enough that
// the caller knows which of the two input paths failed.
func readBody(rc *kernel.RunContext, file string) ([]byte, error) {
	file = strings.TrimSpace(file)
	if file == "" || file == "-" {
		if rc == nil || rc.Stdin == nil {
			return nil, appstorecmd.Usagef("no --file and no stdin to read the hosted app submission from — pass --file <path> or pipe the JSON body on stdin")
		}
		b, err := io.ReadAll(rc.Stdin)
		if err != nil {
			return nil, appstorecmd.Usagef("cannot read the hosted app submission from stdin: %v", err)
		}
		return b, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, appstorecmd.Usagef("cannot read --file %s: %v", file, err)
	}
	return b, nil
}

// parseBody turns the operator's JSON into the request struct. Unknown fields
// are tolerated on purpose (Google grows this schema); the policy responses
// stay json.RawMessage all the way to the wire, so a question type gplay has
// never heard of travels through intact.
func parseBody(body []byte) (appstore.UpdateHostedAppRequest, error) {
	var req appstore.UpdateHostedAppRequest
	if len(bytes.TrimSpace(body)) == 0 {
		return req, appstorecmd.Usagef("the hosted app submission body is empty — pass --file <path> or pipe the JSON body on stdin")
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return req, appstorecmd.Usagef("the hosted app submission body is not a valid UpdateAppStoreHostedAppRequest: %v", err)
	}
	return req, nil
}

// resolveTarget reconciles the package the flags/pin resolve to with the
// packageName the body carries. The flag (then the .gplay/config.json pin) is
// authoritative; a body naming a DIFFERENT app is refused (exit 2) rather than
// silently resolved one way — this call submits to review, so guessing is not
// an option. A body with no packageName inherits the resolved one; a
// self-contained body is accepted when nothing else resolves.
func resolveTarget(rc *kernel.RunContext, flag string, bodyPkg string) (string, error) {
	bodyPkg = strings.TrimSpace(bodyPkg)
	pkg, err := appstorecmd.ResolvePackage(rc, flag)
	if err != nil {
		if bodyPkg == "" {
			return "", err
		}
		// The body names its own subject: honour it rather than demanding the
		// same value twice.
		return bodyPkg, nil
	}
	if bodyPkg != "" && bodyPkg != pkg {
		return "", appstorecmd.Usagef(
			"the submission body names packageName %q but the resolved target is %q — pass --package %s, or remove packageName from the body (the --package flag and the .gplay/config.json pin win, and gplay will not pick between them silently)",
			bodyPkg, pkg, bodyPkg)
	}
	return pkg, nil
}

// Run is the business function the kernel invokes. It reads and validates the
// submission offline first, so a --dry-run rehearsal exercises every check the
// live run does, then gates the irreversible submission behind --confirm.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	storePackage, err := appstorecmd.ResolveStorePackage(in.StorePackage)
	if err != nil {
		return nil, err
	}
	body, err := readBody(rc, in.File)
	if err != nil {
		return nil, err
	}
	req, err := parseBody(body)
	if err != nil {
		return nil, err
	}
	pkg, err := resolveTarget(rc, in.Package, req.PackageName)
	if err != nil {
		return nil, err
	}
	req.PackageName = pkg
	sum := summarize(req)

	// --dry-run short-circuits BEFORE the safety gate (as `customapps create`
	// does): the whole point of the rehearsal is to be runnable without
	// --confirm.
	if in.DryRun {
		return Payload{StorePackage: storePackage, Package: pkg, Sum: sum, DryRun: true}, nil
	}

	// Destructive tier: the call submits the app to Google's review at once —
	// no staging, no recall. --confirm is mandatory (exit 3 naming the flag);
	// CI=true never auto-confirms (gplay never auto-confirms, ADR-0017).
	if !in.Confirm {
		return nil, exit.SafetyFlag("confirm",
			"submitting hosted app %s to Google review is immediate and irrevocable — there is no staging step and no way to recall the submission; pass --confirm to proceed (rehearse first with --dry-run)", pkg)
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	raw, err := appstore.UpdateHostedApp(rc.Ctx, httpClient, storePackage, req)
	if err != nil {
		return nil, appstorecmd.ClassifyReview(storePackage, err)
	}

	// DESIGN §8: a committed mutation prints one ✓ line on stderr; stdout stays
	// data-only.
	rc.Confirmf("submitted hosted app %s in app store %s to Google review (%d locale(s), %d APK set(s), %d policy declaration(s))",
		pkg, storePackage, sum.Locales, sum.ApkSets, sum.PolicyDeclarations)
	return Payload{StorePackage: storePackage, Package: pkg, Sum: sum, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay appstore update`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Submit a hosted app's assembled details to Google review (immediate, irrevocable)",
		Long: `Assemble a hosted app's details — developer identity, active APK sets,
per-locale store listings, policy questionnaire answers — and SUBMIT it to
Google for review, on behalf of a third-party Android app store.

The submission is IMMEDIATE and IRREVOCABLE: Google reviews what this call
sends, there is no staging step and no way to recall it. --confirm is therefore
mandatory (missing → exit 3); CI=true never auto-confirms. Rehearse first with
--dry-run, which reads and validates the file, resolves the target and reports
the counts with ZERO HTTP calls.

The whole request is a JSON file (--file <path>, or "-" / omitted to read
stdin) shaped like UpdateAppStoreHostedAppRequest. There is no flag set for it:
the policy declarations are a growing oneof and are not expressible as flags,
and one versionable file is what a CI job wants anyway. Every id in the body
comes from a prior upload — ` + "`gplay appstore upload apk`" + ` returns the apk ids,
` + "`gplay appstore upload image`" + ` the icon/screenshot ids, and
` + "`gplay appstore upload policy`" + ` the declaration ids.

  {
    "appDetails": {
      "developerName": "Example Inc.",
      "contactEmail": "dev@example.com",
      "developerWebsite": "https://example.com"
    },
    "activeApks": {
      "activeApkSets": [
        { "baseApkId": "apk-base-1", "splitApkId": ["apk-split-1"] }
      ]
    },
    "activeLocalizedStoreListings": [
      {
        "languageCode": "en-US",
        "appName": "Example",
        "shortDescription": "Short blurb",
        "fullDescription": "Full description",
        "appIconId": "img-icon-1",
        "screenshotId": ["img-shot-1", "img-shot-2"],
        "videoLink": "https://youtu.be/xxxxxxxxxxx"
      }
    ],
    "policyDeclarations": [
      {
        "declarationId": "decl-1",
        "responses": [
          { "singleChoiceResponse": { "questionId": "q1", "answerId": "a1" } }
        ]
      }
    ]
  }

Policy responses travel through VERBATIM: gplay does not model the seven-way
PolicyResponse oneof, so a question type it has never seen still submits
correctly.

Addressing uses two identifiers, and mixing them up is the common mistake:

  --store-package  the app store's OWN package name (the caller — the
                   third-party store enrolled for alternative distribution),
                   falling back to $` + appstorecmd.EnvStorePackage + ` (ADR-0043)
  --package        the hosted app's package name (the subject), defaulting to
                   the repo's .gplay/config.json pin when omitted

--package wins over a packageName in the file; a file naming a DIFFERENT app is
refused (exit 2) rather than resolved silently. A file with no packageName
inherits the resolved one.

` + "`gplay appstore create`" + ` must have run for this hosted app first. The call is
Edit-free — it opens no Edit and joins none. The response carries no fields (the
acknowledgement IS the result), so --output json passes the API response
through verbatim (ADR-0003), falling back to a gplay-shaped success object only
when the API answers with no body at all. GPLAY_READONLY refuses the write
(exit 4) but lets --dry-run run. A 403 names the app store enrollment the call
requires.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	appstorecmd.RegisterStorePackageFlag(cmd, &in.StorePackage)
	cmd.Flags().StringVar(&in.Package, "package", "", "package name of the hosted app (overrides .gplay/config.json pin and the body's packageName)")
	cmd.Flags().StringVar(&in.File, "file", "", "path to the JSON UpdateAppStoreHostedAppRequest body (default: stdin, or '-')")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the immediate, irrevocable submission to Google review")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate the file and resolve the target without any HTTP call (reports the --confirm requirement)")
	return cmd
}
