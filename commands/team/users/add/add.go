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
	"github.com/PollyGlot/google-play-cli/internal/ifexists"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	playteam "github.com/PollyGlot/google-play-cli/internal/play/team"
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

type Input struct {
	Email       string
	DeveloperID string
	Role        string
	RoleSet     bool
	Permissions []string
	PermsSet    bool
	GrantAdmin  bool
	DryRun      bool
	IfExists    ifexists.Policy
}

var policies = []ifexists.Policy{ifexists.Skip, ifexists.Update, ifexists.Fail}

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

type Payload struct {
	Email       string
	Permissions []string
	Requires    []string
	Warnings    []string
	DryRun      bool
	Raw         json.RawMessage
	Skipped     bool
	Updated     bool
	IfExists    ifexists.Policy
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) verb() string {
	switch {
	case p.DryRun:
		return "would add"
	case p.Skipped:
		return "skipped (already a member)"
	case p.Updated:
		return "updated (already a member)"
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
	if policy := p.previewPolicy(); policy != "" {
		if _, err := fmt.Fprintf(w, "ifExists: %s\n", policy); err != nil {
			return err
		}
	}
	return nil
}

func (p Payload) previewPolicy() string {
	if !p.DryRun || p.IfExists == "" || p.IfExists == ifexists.Fail {
		return ""
	}
	return string(p.IfExists)
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
	if policy := p.previewPolicy(); policy != "" {
		rows = append(rows, []string{"If exists", policy})
	}
	if _, err := fmt.Fprintf(w, "## team users add%s\n\n", suffix); err != nil {
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
	IfExists    string   `json:"ifExists,omitempty"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{
			DryRun:      true,
			Email:       p.Email,
			Permissions: teamcmd.NonNil(p.Permissions),
			Requires:    teamcmd.NonNil(p.Requires),
			Warnings:    p.Warnings,
			IfExists:    p.previewPolicy(),
		})
	}
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw API payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	email := strings.TrimSpace(in.Email)
	if email == "" {
		return nil, exit.Usagef("missing <email>: usage: gplay team users add <email> --role <bundle>|--permissions <alias,…>")
	}

	if in.RoleSet && in.PermsSet {
		return nil, exit.Usagef("--role and --permissions are mutually exclusive")
	}
	if !in.RoleSet && !in.PermsSet {
		return nil, exit.Usagef("pass --role <bundle> or --permissions <alias,…> (run `gplay team permissions` to list them)")
	}

	policy, err := ifexists.Check(in.IfExists, policies...)
	if err != nil {
		return nil, err
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
			IfExists:    policy,
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

	if policy != ifexists.Fail {
		found, raw, ok, err := playteam.FindUserRaw(rc.Ctx, httpClient, developerID, email)
		if err != nil {
			return nil, classifyError(developerID, email, err)
		}
		switch {
		case ok && policy == ifexists.Skip:
			rc.Logf("%s already exists, skipped (--if-exists skip): permissions unchanged", email)
			return Payload{Email: email, Permissions: found.DeveloperAccountPermissions, Raw: raw, Skipped: true}, nil
		case ok:
			raw, err := teamcmd.SetUserPermissions(rc.Ctx, httpClient, developerID, email, enums)
			if err != nil {
				return nil, err
			}
			rc.Logf("%s already exists, updated (--if-exists update): permissions replaced", email)
			return Payload{Email: email, Permissions: enums, Raw: raw, Updated: true}, nil
		}
	}

	raw, err := playteam.CreateUser(rc.Ctx, httpClient, developerID, email, enums)
	if err != nil {
		return nil, classifyError(developerID, email, err)
	}
	return Payload{Email: email, Permissions: enums, Raw: raw}, nil
}

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
an agent can discover the gate before running it.

Inviting an existing member fails with the API conflict (exit 60) by default.
--if-exists skip or update looks the member up first (users.list): when absent,
the invite runs as usual. When present, skip writes nothing and prints the
member as users.list returned it; update runs the ` + "`gplay team users set`" + ` write
(users.patch, the whole permission set replaced) and prints its response. The
--grant-admin gate applies before the lookup, whatever the policy.`,
		Example: `  # Invite a release manager
  gplay team users add dev@example.com --role release-manager

  # Preview an admin invite: the JSON names the --grant-admin gate
  gplay team users add lead@example.com --role admin --dry-run --output json

  gplay team users add lead@example.com --role admin --grant-admin

  # Converge a member's permissions whether invited yet or not (safe to re-run)
  gplay team users add dev@example.com --role release-manager --if-exists update`,
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
	ifexists.Register(cmd, &in.IfExists, "user", policies...)
	return cmd
}
