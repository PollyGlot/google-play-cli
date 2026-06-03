// Package remove implements `gplay team grants remove <email> --package <pkg>`:
// remove a member's access to one app via grants.delete, targeted by
// email+package path (no pre-read), WITHOUT removing them from the account.
// Destructive tier (ADR-0017): refuses without --confirm (exit 3, naming the
// flag); --dry-run previews the target with no HTTP. CI=true never
// auto-confirms.
package remove

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/team/teamcmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	playteam "github.com/PollyGlot/google-play-cli/internal/play/team"
)

// Input is the request-shaped struct cobra builds from flags + the email arg.
type Input struct {
	Email       string
	Package     string
	DeveloperID string
	Confirm     bool
	DryRun      bool
}

type forbiddenError struct {
	developerID string
	cause       error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not authorized to manage users on developer account %s — grant it Admin (manage-permissions) in the Play Console under Users & permissions: %v", e.developerID, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

type grantNotFoundError struct {
	email string
	pkg   string
	cause error
}

func (e *grantNotFoundError) Error() string {
	return fmt.Sprintf("%s has no grant on %s to remove (already removed, or never granted): %v", e.email, e.pkg, e.cause)
}
func (e *grantNotFoundError) Unwrap() error { return e.cause }

func classifyError(developerID, email, pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{developerID: developerID, cause: err}
		case http.StatusNotFound:
			return &grantNotFoundError{email: email, pkg: pkg, cause: err}
		}
	}
	return err
}

// Payload renders what remove did (or, on --dry-run, would do).
type Payload struct {
	Email    string
	Package  string
	Requires []string
	DryRun   bool
	Raw      json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) verb() string {
	if p.DryRun {
		return "would remove"
	}
	return "removed"
}

func (p Payload) renderTable(w io.Writer) error {
	suffix := ""
	if p.DryRun {
		suffix = " (dry-run)"
	}
	if _, err := fmt.Fprintf(w, "%s %s's access to %s%s\n", p.verb(), p.Email, p.Package, suffix); err != nil {
		return err
	}
	if len(p.Requires) > 0 {
		if _, err := fmt.Fprintf(w, "requires: %s\n", strings.Join(p.Requires, ", ")); err != nil {
			return err
		}
	}
	return nil
}

func (p Payload) renderMarkdown(w io.Writer) error {
	suffix := ""
	if p.DryRun {
		suffix = " (dry-run)"
	}
	rows := [][]string{{"User", p.Email}, {"Package", p.Package}, {"Action", p.verb()}}
	if len(p.Requires) > 0 {
		rows = append(rows, []string{"Requires", strings.Join(p.Requires, ", ")})
	}
	if _, err := fmt.Fprintf(w, "## team grants remove%s\n\n", suffix); err != nil {
		return err
	}
	return output.MarkdownTable(w, []string{"FIELD", "VALUE"}, rows)
}

type dryRunView struct {
	DryRun   bool     `json:"dryRun"`
	Email    string   `json:"email"`
	Package  string   `json:"package"`
	Requires []string `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, Email: p.Email, Package: p.Package, Requires: teamcmd.NonNil(p.Requires)})
	}
	if len(bytes.TrimSpace(p.Raw)) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, struct {
		OK      bool   `json:"ok"`
		Removed string `json:"removed"`
		Package string `json:"package"`
	}{OK: true, Removed: p.Email, Package: p.Package})
}

// Run is the business function the kernel invokes. It enforces the destructive
// --confirm gate (unless --dry-run previews offline) and deletes the grant.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	email := strings.TrimSpace(in.Email)
	pkg := strings.TrimSpace(in.Package)
	if email == "" {
		return nil, exit.Usagef("missing <email> — usage: gplay team grants remove <email> --package <pkg> --confirm")
	}
	if pkg == "" {
		return nil, exit.Usagef("missing --package <pkg> (the app whose access to remove)")
	}

	gate := teamcmd.Gate{Destructive: true}

	if in.DryRun {
		return Payload{Email: email, Package: pkg, Requires: gate.Requires(), DryRun: true}, nil
	}

	if err := gate.Verify(in.Confirm, false); err != nil {
		return nil, err
	}

	developerID, err := teamcmd.DeveloperID(rc, in.DeveloperID)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	raw, err := playteam.DeleteGrant(rc.Ctx, httpClient, developerID, email, pkg)
	if err != nil {
		return nil, classifyError(developerID, email, pkg, err)
	}
	return Payload{Email: email, Package: pkg, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay team grants remove`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "remove <email> --package <pkg>",
		Short: "Remove a member's access to one app (keeps their membership)",
		Long: `Remove <email>'s access to one app (--package) via grants.delete, WITHOUT
removing them from the Developer account — only the per-app grant is deleted.
To off-board a member entirely, use ` + "`gplay team users remove`" + ` instead.

This is destructive, so it refuses without --confirm (exit 3, naming the flag);
CI=true never auto-confirms. Use --dry-run to preview the target with no HTTP
call.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.Email = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.DeveloperID, "developer-id", "", "Play Console Developer account id (overrides the active Account's, env, and project-local)")
	cmd.Flags().StringVar(&in.Package, "package", "", "the app (package name) whose access to remove")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the removal (required — this is destructive)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "preview the target without any HTTP call")
	return cmd
}
