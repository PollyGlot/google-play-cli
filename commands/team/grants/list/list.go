// Package list implements `gplay team grants list`: list the per-app Grants
// across the Developer account's members. The API has no grants.list/get:
// Grants are a field of the User, so this reads the full user list
// (paginated to completion via internal/play/team) and projects the grants.
//
// Optional filters: --package answers "who can touch this app", --user answers
// "what apps this person touches", neither = all grants. It is the 5th `list`
// command and consumes the shared list machinery (ADR-0018 / #93). --output
// json is a faithful {"grants":[…]} projection (there is no native
// grants.list response to pass through verbatim).
package list

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/team/teamcmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	playteam "github.com/PollyGlot/google-play-cli/internal/play/team"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	DeveloperID string
	Package     string
	User        string
	Columns     string
}

type forbiddenError struct {
	developerID string
	cause       error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not authorized to read users on developer account %s: grant it Admin (manage-permissions) in the Play Console under Users & permissions: %v", e.developerID, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

type developerNotFoundError struct {
	developerID string
	cause       error
}

func (e *developerNotFoundError) Error() string {
	return fmt.Sprintf("developer account %s not found, or this credential is not a member of it: verify the developerId in your Play Console URL: %v", e.developerID, e.cause)
}
func (e *developerNotFoundError) Unwrap() error { return e.cause }

func classifyError(developerID string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{developerID: developerID, cause: err}
		case http.StatusNotFound:
			return &developerNotFoundError{developerID: developerID, cause: err}
		}
	}
	return err
}

// GrantRow is one projected per-app grant (member + package + app-level
// permissions). It is NOT a native API shape: it is assembled from each
// User's grants field.
type GrantRow struct {
	Email       string
	Package     string
	Permissions []string
}

// project flattens every member's grants into rows, applying the optional
// package/user filters (case-insensitive on the email, exact on the package).
func project(users []playteam.User, pkgFilter, userFilter string) []GrantRow {
	pkgFilter = strings.TrimSpace(pkgFilter)
	userFilter = strings.ToLower(strings.TrimSpace(userFilter))
	var rows []GrantRow
	for _, u := range users {
		if userFilter != "" && strings.ToLower(u.Email) != userFilter {
			continue
		}
		for _, g := range u.Grants {
			if pkgFilter != "" && g.PackageName != pkgFilter {
				continue
			}
			rows = append(rows, GrantRow{Email: u.Email, Package: g.PackageName, Permissions: g.AppLevelPermissions})
		}
	}
	return rows
}

var columns = output.NewColumnSet(
	output.Column[GrantRow]{Key: "user", Header: "USER", Value: func(r GrantRow) string { return r.Email }},
	output.Column[GrantRow]{Key: "package", Header: "PACKAGE", Value: func(r GrantRow) string { return r.Package }},
	output.Column[GrantRow]{Key: "permissions", Header: "PERMISSIONS", Value: func(r GrantRow) string { return strings.Join(r.Permissions, ",") }},
)

// ResolveColumns turns a --columns spec into validated, ordered columns,
// shared with render tests.
func ResolveColumns(spec string) ([]output.Column[GrantRow], error) {
	return columns.Resolve(spec)
}

type grantJSON struct {
	Email               string   `json:"email"`
	PackageName         string   `json:"packageName"`
	AppLevelPermissions []string `json:"appLevelPermissions"`
}

// Payload satisfies output.Renderable. Rows is the projected view and Columns
// the resolved display order; JSON is a faithful {"grants":[…]} projection.
type Payload struct {
	Rows    []GrantRow
	Columns []output.Column[GrantRow]
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Columns, p.Rows) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Columns, p.Rows) },
	}
}

func (p Payload) renderJSON(w io.Writer) error {
	grants := make([]grantJSON, 0, len(p.Rows))
	for _, r := range p.Rows {
		grants = append(grants, grantJSON{Email: r.Email, PackageName: r.Package, AppLevelPermissions: teamcmd.NonNil(r.Permissions)})
	}
	return output.WriteJSON(w, struct {
		Grants []grantJSON `json:"grants"`
	}{Grants: grants})
}

// Run resolves the developer-id, reads the full user list to completion,
// projects (and filters) the grants, and renders.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	cols, err := ResolveColumns(in.Columns)
	if err != nil {
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
	users, _, err := playteam.ListUsers(rc.Ctx, httpClient, developerID)
	if err != nil {
		return nil, classifyError(developerID, err)
	}
	return Payload{Rows: project(users, in.Package, in.User), Columns: cols}, nil
}

// NewCommand returns the cobra command for `gplay team grants list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List per-app access grants across the Developer account",
		Long: `List the per-app Grants of the Developer account's members: which member has
which app-level permissions on which package.

The Play Developer API has no grants-list endpoint (a Grant is a field of the
User), so this reads the full member list (paginated to completion) and
projects the grants. Filter with --package <pkg> ("who can touch this app") or
--user <email> ("what apps this person touches"); neither lists all grants.

Default table columns: user, package, permissions. --output json is a faithful
{"grants":[…]} projection.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.DeveloperID, "developer-id", "", "Play Console Developer account id (overrides the active Account's, env, and project-local)")
	cmd.Flags().StringVar(&in.Package, "package", "", "filter to one app's grantees (who can touch this app)")
	cmd.Flags().StringVar(&in.User, "user", "", "filter to one member's grants (what apps this person touches)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+strings.Join(columns.DefaultKeys(), ",")+")")
	return cmd
}
