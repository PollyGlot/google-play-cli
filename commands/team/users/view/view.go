// Package view implements `gplay team users view <email>`: a read-only, deep
// view of ONE member of the Developer account, addressed by email — the
// `team users` analogue of `apps view` / `tracks view`. The human views show a
// scalar header (email, accessState, an admin marker, account-wide permissions)
// then the member's Grants one row each; --output json is the matched User
// object verbatim (ADR-0003 pass-through), not a gplay envelope.
//
// The Play Developer API has no users.get and no standalone grants endpoint (a
// Grant is a field of the User), so reading one member means listing users.list
// to completion and filtering by email — the same cost as `team grants list`.
// A member that does not exist is a not-found (exit 30) even though the
// underlying users.list returns HTTP 200: addressing a missing resource mirrors
// `tracks view`/`apps view`. The developer-id resolves through the ADR-0015
// cascade via teamcmd; 403→exit 11 and 404→exit 30 are classified as in
// `team users list`.
package view

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/team/teamcmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	playteam "github.com/PollyGlot/google-play-cli/internal/play/team"
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

// Input is the request-shaped struct cobra builds from the positional email.
type Input struct {
	DeveloperID string
	Email       string
}

// usageError is a CLI-misuse error (empty email); ExitCode()=2 per
// docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// memberNotFoundError is the synthetic not-found for an addressed member that
// no row of users.list matched. The underlying call returns HTTP 200, but
// addressing a member that does not exist is semantically a not-found, so it
// carries ExitCode()=30 (docs/DESIGN.md §9: "API 4xx … not found"), mirroring
// `tracks view`/`apps view`. The message hints at `gplay team users list`.
type memberNotFoundError struct{ email string }

func (e *memberNotFoundError) Error() string {
	return fmt.Sprintf("member %q is not on the Developer account — run `gplay team users list` to see its members", e.email)
}
func (e *memberNotFoundError) ExitCode() int { return 30 }

// forbiddenError wraps a 403 on users.list (the service account lacks the
// account-level permission to read members) with an actionable hint. It carries
// no ExitCode of its own so the wrapped *api.Error (403 → exit 11) stays
// authoritative.
type forbiddenError struct {
	developerID string
	cause       error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not authorized to read users on developer account %s — grant it Admin (manage-permissions) in the Play Console under Users & permissions: %v", e.developerID, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// developerNotFoundError wraps a 404 on users.list (the developer-id is wrong or
// the credential is not a member of it) with a hint. It carries no ExitCode of
// its own so the wrapped *api.Error (404 → exit 30) stays authoritative.
type developerNotFoundError struct {
	developerID string
	cause       error
}

func (e *developerNotFoundError) Error() string {
	return fmt.Sprintf("developer account %s not found, or this credential is not a member of it — verify the developerId in your Play Console URL: %v", e.developerID, e.cause)
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

// grantColumns is the per-grant sub-table shown under the member header: package
// + app-level permissions, one row per grant. It reuses the shared column
// machinery so the table/markdown renders match the rest of the CLI.
var grantColumns = output.NewColumnSet(
	output.Column[playteam.Grant]{Key: "package", Header: "PACKAGE", Value: func(g playteam.Grant) string { return g.PackageName }},
	output.Column[playteam.Grant]{Key: "permissions", Header: "PERMISSIONS", Value: func(g playteam.Grant) string { return strings.Join(g.AppLevelPermissions, ",") }},
)

// Payload satisfies output.Renderable. Raw carries the matched User's verbatim
// bytes for the ADR-0003 JSON pass-through; the typed fields drive the
// human-shaped table and markdown views. None of the typed fields are
// json-tagged — the JSON path is the raw pass-through, never the struct.
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

// adminCell renders the admin marker the same way `team users list` does: "yes"
// for an all-permissions member, empty otherwise.
func adminCell(admin bool) string {
	if admin {
		return "yes"
	}
	return ""
}

// renderTable writes the scalar header as `FIELD<TAB>VALUE` lines (like
// `apps view`), then — when the member has any Grants — the shared grants table
// beneath. A grant-less member shows GRANTS\t0 and no sub-table.
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
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if err := output.RenderTable(tw, cols, p.Grants); err != nil {
		return err
	}
	return tw.Flush()
}

// renderJSON emits the matched User's bytes verbatim (ADR-0003 pass-through).
// Raw is always populated on the Run path; an empty Raw would mean we never
// captured the API body (every Payload field is json:"-"), so we error rather
// than emit zero bytes.
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw users.list payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderMarkdown renders the member as a record: a level-2 heading (the email)
// then a `- **Field**: value` list (docs/DESIGN.md §7), and — when present — the
// grants as a GitHub-Flavored Markdown table, so a pasted report stands alone.
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

// Run is the business function the kernel invokes. It validates the email,
// resolves the developer-id, builds an authenticated client, reads the single
// member (list-and-filter, no users.get), and renders. An absent member is a
// synthetic not-found (exit 30).
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	email := strings.TrimSpace(in.Email)
	if email == "" {
		return nil, &usageError{msg: "no member — pass an email: gplay team users view <email>"}
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

// NewCommand returns the cobra command for `gplay team users view <email>`.
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
User), so this lists the account and filters client-side — the same cost as
` + "`gplay team grants list --user`" + `. A member that is not on the account fails with
exit 30 (run ` + "`gplay team users list`" + ` to see who is).

The Developer account is resolved through the gplay cascade (later wins): the
active Account's developer-id, the project-local .gplay/config.local.json,
GPLAY_DEVELOPER_ID, then --developer-id. An unresolved id fails with exit 10.

--output json is the matched User object verbatim (the exact users.list bytes
for that member, grants included); --output markdown renders a record plus a
grants table.`,
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
