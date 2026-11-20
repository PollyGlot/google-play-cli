package list

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/team/teamcmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	playteam "github.com/PollyGlot/google-play-cli/internal/play/team"
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

type Input struct {
	DeveloperID string
	Columns     string
}

type forbiddenError struct {
	developerID string
	cause       error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not authorized to manage users on developer account %s: in the Play Console, open Users & permissions and grant it Admin (or manage-permissions): %v", e.developerID, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

type developerNotFoundError struct {
	developerID string
	cause       error
}

func (e *developerNotFoundError) Error() string {
	return fmt.Sprintf("developer account %s not found, or this credential is not a member of it: verify the developerId in your Play Console URL (play.google.com/console/u/0/developers/<developerId>/...): %v", e.developerID, e.cause)
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

type UserRow struct {
	Email       string
	AccessState string
	Admin       bool
	Permissions []string
	Grants      int
}

func BuildRows(users []playteam.User) []UserRow {
	rows := make([]UserRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, UserRow{
			Email:       u.Email,
			AccessState: u.AccessState,
			Admin:       vocab.IsAdminConferring(u.DeveloperAccountPermissions),
			Permissions: u.DeveloperAccountPermissions,
			Grants:      len(u.Grants),
		})
	}
	return rows
}

var columns = output.NewColumnSet(
	output.Column[UserRow]{Key: "email", Header: "EMAIL", Value: func(r UserRow) string { return r.Email }},
	output.Column[UserRow]{Key: "access", Header: "ACCESS", Value: func(r UserRow) string { return r.AccessState }},
	output.Column[UserRow]{Key: "admin", Header: "ADMIN", Value: func(r UserRow) string {
		if r.Admin {
			return "yes"
		}
		return ""
	}},
	output.Column[UserRow]{Key: "permissions", Header: "PERMISSIONS", Value: func(r UserRow) string { return strings.Join(r.Permissions, ",") }},
	output.Column[UserRow]{Key: "grants", Header: "GRANTS", Value: func(r UserRow) string { return strconv.Itoa(r.Grants) }},
)

func ResolveColumns(spec string) ([]output.Column[UserRow], error) {
	return columns.Resolve(spec)
}

type Payload struct {
	Raw     json.RawMessage          `json:"-"`
	Rows    []UserRow                `json:"-"`
	Columns []output.Column[UserRow] `json:"-"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Columns, p.Rows) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Columns, p.Rows) },
	}
}

func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw users.list payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

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

	users, raw, err := playteam.ListUsers(rc.Ctx, httpClient, developerID)
	if err != nil {
		return nil, classifyError(developerID, err)
	}

	return Payload{Raw: raw, Rows: BuildRows(users), Columns: cols}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the Developer account's members and their account-wide permissions",
		Long: `List every member of the Developer account with their account-wide
permissions, reading users.list paginated to completion (no silent
truncation). Account-wide admins (the all-permissions permission) are marked
in the ADMIN column.

The Developer account is resolved through the gplay cascade (later wins):
the active Account's developer-id, the project-local .gplay/config.local.json,
GPLAY_DEVELOPER_ID, then --developer-id. Set one with ` + "`gplay auth login --developer-id`" + `;
an unresolved id fails with exit 10.

Default table columns: email, access, admin, permissions, grants. Override
with --columns email,admin,...  --output json is the verbatim users.list
response (a single {"users":[…]} object across all pages).`,
		Example: `  gplay team users list
  gplay team users list --columns email,admin
  gplay team users list --developer-id 1234567890123456789 --output json`,
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
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns to show (default: "+strings.Join(columns.DefaultKeys(), ",")+")")
	return cmd
}
