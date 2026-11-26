package set

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
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

const (
	verbCreate = "create"
	verbUpdate = "update"
)

type Input struct {
	Email       string
	Package     string
	DeveloperID string
	Role        string
	RoleSet     bool
	Permissions []string
	PermsSet    bool
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

type notMemberError struct{ email string }

func (e *notMemberError) Error() string {
	return fmt.Sprintf("%s is not a member of this developer account: add them with `gplay team users add %s` before granting per-app access", e.email, e.email)
}
func (e *notMemberError) ExitCode() int { return 30 }

func classifyError(developerID string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusForbidden {
			return &forbiddenError{developerID: developerID, cause: err}
		}
	}
	return err
}

type Payload struct {
	Email    string
	Package  string
	Verb     string
	Current  []string
	Desired  []string
	Requires []string
	Warnings []string
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

func (p Payload) verbLabel() string {
	if p.DryRun {
		return "would " + p.Verb
	}
	if p.Verb == verbCreate {
		return "created"
	}
	return "updated"
}

func (p Payload) renderTable(w io.Writer) error {
	suffix := ""
	if p.DryRun {
		suffix = " (dry-run)"
	}
	if _, err := fmt.Fprintf(w, "%s grant for %s on %s%s\n", p.verbLabel(), p.Email, p.Package, suffix); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "permissions: %s -> %s\n", teamcmd.JoinOrNone(p.Current), teamcmd.JoinOrNone(p.Desired)); err != nil {
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
		{"Package", p.Package},
		{"Action", p.verbLabel()},
		{"Current", teamcmd.JoinOrNone(p.Current)},
		{"Desired", teamcmd.JoinOrNone(p.Desired)},
	}
	if len(p.Requires) > 0 {
		rows = append(rows, []string{"Requires", strings.Join(p.Requires, ", ")})
	}
	if _, err := fmt.Fprintf(w, "## team grants set%s\n\n", suffix); err != nil {
		return err
	}
	return output.MarkdownTable(w, []string{"FIELD", "VALUE"}, rows)
}

type dryRunView struct {
	DryRun   bool     `json:"dryRun"`
	Email    string   `json:"email"`
	Package  string   `json:"package"`
	Verb     string   `json:"verb"`
	Current  []string `json:"current"`
	Desired  []string `json:"desired"`
	Add      []string `json:"add"`
	Remove   []string `json:"remove"`
	Requires []string `json:"requires"`
	Warnings []string `json:"warnings,omitempty"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		add, remove := diff(p.Current, p.Desired)
		return output.WriteJSON(w, dryRunView{
			DryRun:   true,
			Email:    p.Email,
			Package:  p.Package,
			Verb:     p.Verb,
			Current:  teamcmd.NonNil(p.Current),
			Desired:  teamcmd.NonNil(p.Desired),
			Add:      add,
			Remove:   remove,
			Requires: teamcmd.NonNil(p.Requires),
			Warnings: p.Warnings,
		})
	}
	_, err := w.Write(p.Raw)
	return err
}

func diff(current, desired []string) (add, remove []string) {
	cur := make(map[string]bool, len(current))
	for _, c := range current {
		cur[c] = true
	}
	des := make(map[string]bool, len(desired))
	for _, d := range desired {
		des[d] = true
	}
	add = []string{}
	for d := range des {
		if !cur[d] {
			add = append(add, d)
		}
	}
	remove = []string{}
	for c := range cur {
		if !des[c] {
			remove = append(remove, c)
		}
	}
	sort.Strings(add)
	sort.Strings(remove)
	return add, remove
}

func currentGrant(u *playteam.User, pkg string) ([]string, bool) {
	for _, g := range u.Grants {
		if g.PackageName == pkg {
			return g.AppLevelPermissions, true
		}
	}
	return nil, false
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	email := strings.TrimSpace(in.Email)
	pkg := strings.TrimSpace(in.Package)
	if email == "" {
		return nil, exit.Usagef("missing <email>: usage: gplay team grants set <email> --package <pkg> --role <bundle>|--permissions <alias,…>")
	}
	if pkg == "" {
		return nil, exit.Usagef("missing --package: pass --package <pkg> (the app to grant access to)")
	}
	if in.RoleSet && in.PermsSet {
		return nil, exit.Usagef("--role and --permissions are mutually exclusive")
	}
	if !in.RoleSet && !in.PermsSet {
		return nil, exit.Usagef("pass --role <bundle> or --permissions <alias,…> (run `gplay team permissions --scope app` to list them)")
	}

	if err := teamcmd.RefuseBlankSelector(in.RoleSet, in.Role, in.PermsSet, in.Permissions, "pass a non-empty --role <bundle> or --permissions <alias,…> (run `gplay team permissions --scope app` to list them)"); err != nil {
		return nil, err
	}

	enums, warnings, err := teamcmd.ResolvePerms(vocab.App, in.Role, in.Permissions, false)
	if err != nil {
		return nil, err
	}
	teamcmd.EmitWarnings(rc, warnings)

	gate := teamcmd.Gate{AdminConferring: vocab.IsAdminConferring(enums)}
	if !in.DryRun {
		if err := gate.Verify(false, in.GrantAdmin); err != nil {
			return nil, err
		}
	}

	developerID, err := teamcmd.DeveloperID(rc, in.DeveloperID)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	user, found, err := playteam.FindUser(rc.Ctx, httpClient, developerID, email)
	if err != nil {
		return nil, classifyError(developerID, err)
	}
	if !found {
		return nil, &notMemberError{email: email}
	}
	current, exists := currentGrant(user, pkg)
	verb := verbCreate
	if exists {
		verb = verbUpdate
	}

	if in.DryRun {
		return Payload{
			Email:    email,
			Package:  pkg,
			Verb:     verb,
			Current:  current,
			Desired:  enums,
			Requires: gate.Requires(),
			Warnings: warnings,
			DryRun:   true,
		}, nil
	}

	var raw json.RawMessage
	if exists {
		raw, err = playteam.PatchGrant(rc.Ctx, httpClient, developerID, email, pkg, enums)
	} else {
		raw, err = playteam.CreateGrant(rc.Ctx, httpClient, developerID, email, pkg, enums)
	}
	if err != nil {
		return nil, classifyError(developerID, err)
	}
	return Payload{Email: email, Package: pkg, Verb: verb, Current: current, Desired: enums, Raw: raw}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "set <email> --package <pkg>",
		Short: "Grant or adjust a member's per-app access (upsert)",
		Long: `Grant or adjust <email>'s access to one app (--package) via an upsert:
gplay reads the member's current grants, then creates the grant if absent or
updates it if present. Express permissions in friendly form: --role <bundle>
XOR --permissions <alias,…>: resolved in app scope. Run
` + "`gplay team permissions --scope app`" + ` to list them.

Use --dry-run to rehearse: it reads the current state (an idempotent read, no
write) and, with --output json, reports the resolved verb (create/update), the
permission diff (current → desired, with add/remove), and the ` + "`requires`" + ` array.
An admin-conferring grant requires the named --grant-admin (exit 3).`,
		Example: `  # Let QA manage testers on one app
  gplay team grants set qa@example.com --package com.example.app --role tester-manager

  # Preview a hand-picked permission set: verb, diff and required flags
  gplay team grants set dev@example.com --package com.example.app \
    --permissions release-testing,reply-reviews --dry-run --output json`,
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
	cmd.Flags().StringVar(&in.Package, "package", "", "the app (package name) to grant access to")
	cmd.Flags().StringVar(&in.Role, "role", "", "role bundle to grant (viewer, reviewer, tester-manager, release-manager, admin)")
	cmd.Flags().StringSliceVar(&in.Permissions, "permissions", nil, "permission aliases or raw CAN_* enums (repeatable or comma-separated)")
	cmd.Flags().BoolVar(&in.GrantAdmin, "grant-admin", false, "acknowledge conferring admin (required when the permission set includes admin)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "rehearse: read current state and report the verb + diff without writing")
	return cmd
}
