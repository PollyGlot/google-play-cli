// Package set implements `gplay team users set <email>`: replace a member's
// account-wide permissions declaratively via users.patch (like `testers set`).
// It is the routine tier (ADR-0017): --role XOR --permissions, no confirmation
// gate for a normal replace, even a permission-REDUCING set is a previewable
// declarative statement, not a separately gated event.
//
// A bare `set` (neither --role nor --permissions nor --clear) is a misuse
// (exit 2) so permissions can never be silently blanked; emptying on purpose is
// the explicit --clear. Conferring admin still requires the named --grant-admin
// (exit 3). Reuses the write scaffold (dry-run / `requires` / gate detection)
// from #152.
package set

import (
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
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

// Input is the request-shaped struct cobra builds from flags + the email arg.
type Input struct {
	Email       string
	DeveloperID string
	Role        string
	RoleSet     bool
	Permissions []string
	PermsSet    bool
	Clear       bool
	GrantAdmin  bool
	DryRun      bool
}

type forbiddenError struct {
	developerID string
	cause       error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not authorized to manage users on developer account %s: grant it Admin (manage-permissions) in the Play Console under Users & permissions: %v", e.developerID, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

type notMemberError struct {
	email string
	cause error
}

func (e *notMemberError) Error() string {
	return fmt.Sprintf("%s is not a member: use `gplay team users add %s` to invite them first: %v", e.email, e.email, e.cause)
}
func (e *notMemberError) Unwrap() error { return e.cause }

func classifyError(developerID, email string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{developerID: developerID, cause: err}
		case http.StatusNotFound:
			return &notMemberError{email: email, cause: err}
		}
	}
	return err
}

// Payload renders what set did (or, on --dry-run, would do).
type Payload struct {
	Email       string
	Permissions []string
	Requires    []string
	Warnings    []string
	DryRun      bool
	Raw         json.RawMessage
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
		return "would set"
	}
	return "set"
}

func (p Payload) renderTable(w io.Writer) error {
	suffix := ""
	if p.DryRun {
		suffix = " (dry-run)"
	}
	if _, err := fmt.Fprintf(w, "%s %s%s\n", p.verb(), p.Email, suffix); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "permissions: %s\n", teamcmd.JoinOrNone(p.Permissions)); err != nil {
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
	rows := [][]string{
		{"User", p.Email},
		{"Action", p.verb()},
		{"Permissions", teamcmd.JoinOrNone(p.Permissions)},
	}
	if len(p.Requires) > 0 {
		rows = append(rows, []string{"Requires", strings.Join(p.Requires, ", ")})
	}
	if _, err := fmt.Fprintf(w, "## team users set%s\n\n", suffix); err != nil {
		return err
	}
	return output.MarkdownTable(w, []string{"FIELD", "VALUE"}, rows)
}

type dryRunView struct {
	DryRun      bool     `json:"dryRun"`
	Email       string   `json:"email"`
	Permissions []string `json:"permissions"`
	Requires    []string `json:"requires"`
	Warnings    []string `json:"warnings,omitempty"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{
			DryRun:      true,
			Email:       p.Email,
			Permissions: teamcmd.NonNil(p.Permissions),
			Requires:    teamcmd.NonNil(p.Requires),
			Warnings:    p.Warnings,
		})
	}
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw users.patch payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// selectorCount counts how many of the mutually-exclusive permission
// selectors were set.
func selectorCount(in Input) int {
	n := 0
	for _, set := range []bool{in.RoleSet, in.PermsSet, in.Clear} {
		if set {
			n++
		}
	}
	return n
}

// Run is the business function the kernel invokes. It enforces the declarative
// footgun guard, resolves the replacement permission set, applies the gate, and
// (unless --dry-run short-circuits) replaces the member's permissions.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	email := strings.TrimSpace(in.Email)
	if email == "" {
		return nil, exit.Usagef("missing <email>: usage: gplay team users set <email> --role <bundle>|--permissions <alias,…>|--clear")
	}

	switch selectorCount(in) {
	case 1:
		// ok
	case 0:
		// Footgun guard: a bare set must never silently blank permissions.
		return nil, exit.Usagef("refusing to change permissions without --role, --permissions, or --clear (a forgotten flag must not silently blank the set); pass --role <bundle> / --permissions <alias,…> to declare them, or --clear to empty them")
	default:
		return nil, exit.Usagef("--role, --permissions, and --clear are mutually exclusive")
	}

	enums, warnings, err := teamcmd.ResolvePerms(vocab.Account, in.Role, in.Permissions, in.Clear)
	if err != nil {
		return nil, err
	}
	teamcmd.EmitWarnings(rc, warnings)

	gate := teamcmd.Gate{AdminConferring: vocab.IsAdminConferring(enums)}

	if in.DryRun {
		return Payload{
			Email:       email,
			Permissions: enums,
			Requires:    gate.Requires(),
			Warnings:    warnings,
			DryRun:      true,
		}, nil
	}

	if err := gate.Verify(false, in.GrantAdmin); err != nil {
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

	raw, err := playteam.SetUserPermissions(rc.Ctx, httpClient, developerID, email, enums)
	if err != nil {
		return nil, classifyError(developerID, email, err)
	}
	return Payload{Email: email, Permissions: enums, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay team users set`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "set <email>",
		Short: "Replace a member's account-wide permissions (declarative)",
		Long: `Replace <email>'s account-wide permissions declaratively via users.patch:
the whole set is sent, not merged. Express the set in friendly form:
--role <bundle> XOR --permissions <alias,…> (account scope). Run
` + "`gplay team permissions`" + ` to list aliases and bundles.

A bare ` + "`set`" + ` (no --role, --permissions, or --clear) is refused (exit 2) so a
forgotten flag can never silently blank the permissions; empty them on purpose
with --clear. A permission-reducing set is a normal previewable statement (not
gated); conferring admin still requires the named --grant-admin (exit 3).

Use --dry-run to preview the resolved payload with no HTTP; with --output json
it emits a ` + "`requires`" + ` array naming any safety flag the live write needs.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.Email = args[0]
			in.RoleSet = cmd.Flags().Changed("role")
			in.PermsSet = cmd.Flags().Changed("permissions")
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.DeveloperID, "developer-id", "", "Play Console Developer account id (overrides the active Account's, env, and project-local)")
	cmd.Flags().StringVar(&in.Role, "role", "", "role bundle to set (viewer, reviewer, tester-manager, release-manager, admin)")
	cmd.Flags().StringSliceVar(&in.Permissions, "permissions", nil, "permission aliases or raw CAN_* enums (repeatable or comma-separated)")
	cmd.Flags().BoolVar(&in.Clear, "clear", false, "replace the permission set with an empty set")
	cmd.Flags().BoolVar(&in.GrantAdmin, "grant-admin", false, "acknowledge conferring admin (required when the permission set includes admin)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "preview the resolved payload without any HTTP call")
	return cmd
}
