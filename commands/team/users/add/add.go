// Package add implements `gplay team users add <email>`: invite a member with
// account-wide permissions via users.create. Permissions are expressed in
// friendly form (--role <bundle> XOR --permissions <alias,…>) resolved by
// the vocabulary module in account scope (it appends _GLOBAL).
//
// add lands the write-safety scaffold the other `team` writes reuse (ADR-0017):
// the exit-3 "safety flag required" code, --dry-run previewing the resolved
// payload offline plus a machine-readable `requires` array, and the named
// --grant-admin gate for admin-conferring writes. add itself is the routine
// tier (no gate) unless it confers admin. CI=true never auto-confirms a gate.
package add

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
// RoleSet/PermsSet record whether the flag was passed (cmd.Flags().Changed) so
// the XOR guard distinguishes "unset" from "set empty".
type Input struct {
	Email       string
	DeveloperID string
	Role        string
	RoleSet     bool
	Permissions []string
	PermsSet    bool
	GrantAdmin  bool
	DryRun      bool
}

// forbiddenError / alreadyMemberError attach actionable hints to a 403 / 409 on
// users.create, leaving the wrapped *api.Error to drive the exit code
// (403→11, 409→60).
type forbiddenError struct {
	developerID string
	cause       error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not authorized to manage users on developer account %s: grant it Admin (manage-permissions) in the Play Console under Users & permissions: %v", e.developerID, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

type alreadyMemberError struct {
	email string
	cause error
}

func (e *alreadyMemberError) Error() string {
	return fmt.Sprintf("%s is already a member: use `gplay team users set %s` to change their permissions: %v", e.email, e.email, e.cause)
}
func (e *alreadyMemberError) Unwrap() error { return e.cause }

func classifyError(developerID, email string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{developerID: developerID, cause: err}
		case http.StatusConflict:
			return &alreadyMemberError{email: email, cause: err}
		}
	}
	return err
}

// Payload renders what add did (or, on --dry-run, would do). On a dry-run it is
// the offline preview (the resolved permissions + the `requires` array); on a
// real write Raw carries the users.create response for the ADR-0003 --output
// json pass-through.
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
		return "would add"
	}
	return "added"
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
	if _, err := fmt.Fprintf(w, "## team users add%s\n\n", suffix); err != nil {
		return err
	}
	return output.MarkdownTable(w, []string{"FIELD", "VALUE"}, rows)
}

// dryRunView is the gplay-shaped --dry-run JSON: the resolved payload plus the
// machine-readable `requires` array (ADR-0017 §4). A live write emits the API
// response verbatim instead.
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
		return fmt.Errorf("missing raw users.create payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// Run is the business function the kernel invokes. It validates the flag
// combination, resolves the permissions in account scope, applies the
// write-safety gate, and, unless --dry-run short-circuits before any network,
// creates the member.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	email := strings.TrimSpace(in.Email)
	if email == "" {
		return nil, exit.Usagef("missing <email>: usage: gplay team users add <email> --role <bundle>|--permissions <alias,…>")
	}

	// --role XOR --permissions: exactly one is required for add (a member must
	// be invited with some access; emptying is a `set --clear` concern).
	if in.RoleSet && in.PermsSet {
		return nil, exit.Usagef("--role and --permissions are mutually exclusive")
	}
	if !in.RoleSet && !in.PermsSet {
		return nil, exit.Usagef("pass --role <bundle> or --permissions <alias,…> (run `gplay team permissions` to list them)")
	}

	enums, warnings, err := teamcmd.ResolvePerms(vocab.Account, in.Role, in.Permissions, false)
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

	// Live path: enforce the gate before any network so a missing flag fails
	// instantly, never after a write.
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

	raw, err := playteam.CreateUser(rc.Ctx, httpClient, developerID, email, enums)
	if err != nil {
		return nil, classifyError(developerID, email, err)
	}
	return Payload{Email: email, Permissions: enums, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay team users add`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "add <email>",
		Short: "Invite a member with account-wide permissions",
		Long: `Invite <email> as a member of the Developer account with account-wide
permissions, via users.create. Express permissions in friendly form:
--role <bundle> XOR --permissions <alias,…>: resolved in account scope
(the _GLOBAL family). Run ` + "`gplay team permissions`" + ` to list aliases and bundles.

add is the routine tier: no confirmation gate, and CI-scriptable. But an
admin-conferring add (--role admin, or a permission set including the
all-permissions enum) requires the named --grant-admin: handing out full
control is never silent.

Use --dry-run to preview the resolved payload with no HTTP; with --output json
it emits a ` + "`requires`" + ` array naming the safety flags the live write needs, so
an agent can discover the gate before running it.`,
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
	cmd.Flags().StringVar(&in.Role, "role", "", "role bundle to grant (viewer, reviewer, tester-manager, release-manager, admin)")
	cmd.Flags().StringSliceVar(&in.Permissions, "permissions", nil, "permission aliases or raw CAN_* enums (repeatable or comma-separated)")
	cmd.Flags().BoolVar(&in.GrantAdmin, "grant-admin", false, "acknowledge conferring admin (required when the permission set includes admin)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "preview the resolved payload without any HTTP call")
	return cmd
}
