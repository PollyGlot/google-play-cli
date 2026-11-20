package view

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
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	playteam "github.com/PollyGlot/google-play-cli/internal/play/team"
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

type Input struct {
	DeveloperID string
	Email       string
}

type memberNotFoundError struct{ email string }

func (e *memberNotFoundError) Error() string {
	return fmt.Sprintf("member %q is not on the Developer account: run `gplay team users list` to see its members", e.email)
}
func (e *memberNotFoundError) ExitCode() int { return 30 }

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

var grantColumns = output.NewColumnSet(
	output.Column[playteam.Grant]{Key: "package", Header: "PACKAGE", Value: func(g playteam.Grant) string { return g.PackageName }},
	output.Column[playteam.Grant]{Key: "permissions", Header: "PERMISSIONS", Value: func(g playteam.Grant) string { return strings.Join(g.AppLevelPermissions, ",") }},
)

type Payload struct {
	Email       string           `json:"-"`
	AccessState string           `json:"-"`
	Admin       bool             `json:"-"`
	Permissions []string         `json:"-"`
	Grants      []playteam.Grant `json:"-"`
	Raw         json.RawMessage  `json:"-"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func adminCell(admin bool) string {
	if admin {
		return "yes"
	}
	return ""
}

func renderTable(w io.Writer, p Payload) error {
	rows := [][2]string{
		{"EMAIL", p.Email},
		{"ACCESS_STATE", p.AccessState},
		{"ADMIN", adminCell(p.Admin)},
		{"PERMISSIONS", strings.Join(p.Permissions, ",")},
		{"GRANTS", strconv.Itoa(len(p.Grants))},
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", r[0], r[1]); err != nil {
			return err
		}
	}
	if len(p.Grants) == 0 {
		return nil
	}
	cols, err := grantColumns.Resolve("")
	if err != nil {
		return err
	}
	return output.RenderTable(w, cols, p.Grants)
}

func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw users.list payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

func renderMarkdown(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "## %s\n\n- **Access state**: %s\n- **Admin**: %s\n- **Permissions**: %s\n- **Grants**: %d\n",
		p.Email, p.AccessState, adminCell(p.Admin), strings.Join(p.Permissions, ", "), len(p.Grants)); err != nil {
		return err
	}
	if len(p.Grants) == 0 {
		return nil
	}
	cols, err := grantColumns.Resolve("")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return output.RenderMarkdown(w, cols, p.Grants)
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	email := strings.TrimSpace(in.Email)
	if email == "" {
		return nil, exit.Usagef("no member: pass an email: gplay team users view <email>")
	}

	developerID, err := teamcmd.DeveloperID(rc, in.DeveloperID)
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	user, raw, found, err := playteam.FindUserRaw(rc.Ctx, httpClient, developerID, email)
	if err != nil {
		return nil, classifyError(developerID, err)
	}
	if !found {
		return nil, &memberNotFoundError{email: email}
	}

	return Payload{
		Email:       user.Email,
		AccessState: user.AccessState,
		Admin:       vocab.IsAdminConferring(user.DeveloperAccountPermissions),
		Permissions: user.DeveloperAccountPermissions,
		Grants:      user.Grants,
		Raw:         raw,
	}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view <email>",
		Short: "Show one Developer-account member: account-wide permissions and per-app grants",
		Long: `Show a single member of the Developer account, addressed by email: their
accessState, account-wide permissions (account-wide admins are marked in the
ADMIN field), and their per-app Grants one row each.

The Play Developer API has no users.get (a member's Grants are a field of the
User), so this lists the account and filters client-side: the same cost as
` + "`gplay team grants list --user`" + `. A member that is not on the account fails with
exit 30 (run ` + "`gplay team users list`" + ` to see who is).

The Developer account is resolved through the gplay cascade (later wins): the
active Account's developer-id, the project-local .gplay/config.local.json,
GPLAY_DEVELOPER_ID, then --developer-id. An unresolved id fails with exit 10.

--output json is the matched User object verbatim (the exact users.list bytes
for that member, grants included); --output markdown renders a record plus a
grants table.`,
		Example: `  gplay team users view dev@example.com
  gplay team users view dev@example.com --output json`,
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
	return cmd
}
