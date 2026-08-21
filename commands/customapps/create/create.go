// Package create implements `gplay customapps create`: create a private app
// distributed to one organisation through managed Google Play, via
// playcustomapp.accounts.customApps.create: the one Developer API path that
// creates an app *record* (ADR-0032). It is keyed by the developer-account axis
// (accounts/{account}, ADR-0015), not a package: the app does not yet exist to
// be keyed by package. A resumable AAB/APK upload carries the CustomApp
// metadata (--title, --default-language, repeatable --organization) in the
// initiate request, then streams the artifact in chunks (PRD #355).
//
// Creation is IRREVERSIBLE: the API exposes no delete (and no read), so the
// app permanently occupies the account. That puts it in the destructive tier
// (ADR-0017): --confirm is required (missing → exit 3 naming the flag), CI=true
// never auto-confirms, and --dry-run rehearses offline. The account must be
// enrolled in managed Google Play and the service account must hold the
// account-level CAN_CREATE_MANAGED_PLAY_APPS capability; a 403 surfaces as an
// agent-resolvable refusal naming both. Ships [experimental] (ADR-0010);
// companion skill per ADR-0021.
package create

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/customapps"
	"github.com/PollyGlot/google-play-cli/internal/team/addressing"
)

// Input is the request-shaped struct cobra builds from flags + args.
type Input struct {
	DeveloperID   string
	Title         string
	Language      string
	Organizations []string // repeatable --organization (organization IDs)
	ArtifactPath  string
	Confirm       bool
	DryRun        bool
}

// usageError is CLI misuse (exit 2): a missing required flag or artifact path.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// localFileError is a client-side artifact-validation failure (exit 20),
// matching the *customapps.LocalIOError the live uploader returns, so the same
// exit code applies whether the artifact is rejected here (offline pre-check /
// --dry-run) or inside Create.
type localFileError struct {
	path  string
	msg   string
	cause error
}

func (e *localFileError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.path, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.path, e.msg)
}
func (e *localFileError) Unwrap() error { return e.cause }
func (e *localFileError) ExitCode() int { return 20 }

// notEnrolledError wraps a 403 on customApps.create (the account is not enrolled
// in managed Google Play, or the service account lacks CAN_CREATE_MANAGED_PLAY_APPS)
// with an actionable hint. It carries no ExitCode of its own so the wrapped
// *api.Error (403 → exit 11) stays authoritative.
type notEnrolledError struct {
	account string
	cause   error
}

func (e *notEnrolledError) Error() string {
	return fmt.Sprintf("developer account %s cannot create custom apps: enroll it in managed Google Play and grant the service account the account-level CAN_CREATE_MANAGED_PLAY_APPS capability (Play Console → Users & permissions), then retry: %v", e.account, e.cause)
}
func (e *notEnrolledError) Unwrap() error { return e.cause }

func classifyError(account string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return &notEnrolledError{account: account, cause: err}
	}
	return err
}

// validateArtifact rejects a missing / non-regular artifact offline (exit 20)
// so --dry-run and the live pre-check fail the same way without any HTTP call.
func validateArtifact(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return &localFileError{path: path, cause: err}
	}
	if !info.Mode().IsRegular() {
		return &localFileError{path: path, msg: "not a regular file"}
	}
	return nil
}

// organizations maps the repeatable --organization values (organization IDs)
// to the API shape, trimming blanks.
func organizations(ids []string) []customapps.Organization {
	var out []customapps.Organization
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, customapps.Organization{OrganizationID: id})
		}
	}
	return out
}

// Payload renders the created custom app (live) or the rehearsal (--dry-run).
type Payload struct {
	Account string
	Title   string
	Lang    string
	Orgs    []customapps.Organization
	DryRun  bool
	App     customapps.CustomApp
	Raw     json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) renderTable(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "would create custom app %q (default language %s) on developer account %s (dry-run); requires --confirm\n", p.Title, p.Lang, p.Account)
		return err
	}
	// packageName leads: it is the identity of the app the call created.
	if _, err := fmt.Fprintf(w, "packageName:   %s\n", p.App.PackageName); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "title:         %s\n", p.App.Title); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "languageCode:  %s\n", p.App.LanguageCode)
	return err
}

// dryRunView is the gplay-shaped dry-run report carrying the ADR-0017 `requires`
// array so an agent learns the gate before running live.
type dryRunView struct {
	DryRun        bool                      `json:"dryRun"`
	Account       string                    `json:"account"`
	Title         string                    `json:"title"`
	LanguageCode  string                    `json:"languageCode"`
	Organizations []customapps.Organization `json:"organizations,omitempty"`
	Requires      []string                  `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{
			DryRun:        true,
			Account:       p.Account,
			Title:         p.Title,
			LanguageCode:  p.Lang,
			Organizations: p.Orgs,
			Requires:      []string{"confirm"},
		})
	}
	// Live: verbatim API pass-through of the created CustomApp (ADR-0003).
	_, err := w.Write(p.Raw)
	return err
}

func (p Payload) renderMarkdown(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "- **dry-run**: would create custom app `%s` (default language %s) on account `%s` (requires --confirm)\n", p.Title, p.Lang, p.Account)
		return err
	}
	if _, err := fmt.Fprintf(w, "- **packageName**: `%s`\n", p.App.PackageName); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "- **title**: %s\n", p.App.Title); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "- **languageCode**: %s\n", p.App.LanguageCode)
	return err
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if strings.TrimSpace(in.ArtifactPath) == "" {
		return nil, &usageError{msg: "missing artifact path: gplay customapps create --title … --default-language … <app.aab|app.apk> --confirm"}
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, &usageError{msg: "missing --title: the custom app's display title is required"}
	}
	if strings.TrimSpace(in.Language) == "" {
		return nil, &usageError{msg: "missing --default-language: the default listing language (BCP 47, e.g. en-US) is required"}
	}

	account, err := addressing.Resolve(in.DeveloperID, os.Getenv(addressing.EnvDeveloperID), rc.Resolved)
	if err != nil {
		return nil, err
	}

	if err := validateArtifact(in.ArtifactPath); err != nil {
		return nil, err
	}

	orgs := organizations(in.Organizations)

	if in.DryRun {
		// Full rehearsal minus the upload: inputs + artifact validated, zero HTTP.
		return Payload{Account: account, Title: in.Title, Lang: in.Language, Orgs: orgs, DryRun: true}, nil
	}

	// Destructive/irreversible tier: --confirm is mandatory (exit 3 naming the
	// flag); CI=true never auto-confirms (gplay never auto-confirms, ADR-0017).
	if !in.Confirm {
		return nil, exit.SafetyFlag("confirm", "creating a custom app is irreversible (the API exposes no delete); pass --confirm to proceed (rehearse first with --dry-run)")
	}

	// UploadClient, not AuthedClient: the artifact can be multiple GB, so it is
	// exempt from the 60s control-plane default (honors an explicit --timeout).
	httpClient, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}

	app, raw, err := customapps.Create(rc.Ctx, httpClient, account, in.ArtifactPath, customapps.CreateOpts{
		Title:         in.Title,
		LanguageCode:  in.Language,
		Organizations: orgs,
	})
	if err != nil {
		return nil, classifyError(account, err)
	}

	// DESIGN §8: a committed mutation prints one ✓ line on stderr (never on --dry-run).
	rc.Confirmf("created custom app %q on developer account %s: packageName %s", app.Title, account, app.PackageName)
	return Payload{Account: account, App: app, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay customapps create`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "create <app.aab|app.apk>",
		Short: "Create a managed Google Play private app (irreversible)",
		Long: `Create a private app distributed to one organisation through managed Google
Play, uploading the AAB/APK with the custom app's metadata in a single call.
This is the one Google Play Developer API path that creates an app record
(public apps can only be created in the Play Console).

The app is created on the Developer account (the account axis, not a package):
resolve the account through the gplay cascade (later wins): the active
Account's developer-id, .gplay/config.local.json, GPLAY_DEVELOPER_ID, then
--developer-id; an unresolved id fails with exit 10.

Pass --title, --default-language (BCP 47, e.g. en-US), and the artifact path.
Restrict the app to specific organizations with a repeatable --organization
<organizationId> (omit to default to the organization linked to the account).

Creating a custom app is IRREVERSIBLE: the API exposes no delete (and no
read), so the app permanently occupies the account. It therefore requires
--confirm (missing → exit 3); CI=true never auto-confirms. Rehearse first with
--dry-run (validates inputs and the artifact with zero HTTP). The account must
be enrolled in managed Google Play and the service account must hold the
account-level CAN_CREATE_MANAGED_PLAY_APPS capability; a 403 names both.

--output json passes the created CustomApp through verbatim (including the
output-only packageName). GPLAY_READONLY refuses the live write (exit 4).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.ArtifactPath = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.DeveloperID, "developer-id", "", "Play Console Developer account id (overrides the active Account's, env, and project-local)")
	cmd.Flags().StringVar(&in.Title, "title", "", "the custom app's display title (required)")
	cmd.Flags().StringVar(&in.Language, "default-language", "", "default listing language in BCP 47 (e.g. en-US) (required)")
	cmd.Flags().StringArrayVar(&in.Organizations, "organization", nil, "restrict to this organization id (repeatable; omit to default to the account's organization)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the irreversible app creation")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and the artifact without any HTTP call (reports the --confirm requirement)")
	return cmd
}
