// Package permissions implements `gplay team permissions`: an OFFLINE leaf (no
// API, no auth, zero network) that publishes gplay's permission vocabulary:
// the curated aliases (alias → account `_GLOBAL` enum → app enum → label →
// including-bundles) and the frozen role bundles. It is the in-terminal
// discovery surface (US17) and the contract snapshot for the vocabulary module
// (ADR-0016): the same `internal/team/vocab` package that every `team` write
// resolves --role / --permissions through.
//
// --scope account|app selects which enum family is resolved (account
// `_GLOBAL` vs bare app-level); --output json marks admin-conferring aliases
// and bundles for machine consumers (ADR-0017 §5).
package permissions

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

// appEnumNone is the table/markdown placeholder for an account-only alias that
// has no per-app enum.
const appEnumNone = "n/a"

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Scope string
}

// aliasRow is one rendered alias line (table/markdown).
type aliasRow struct {
	alias       string
	accountEnum string
	appEnum     string
	bundles     string
	label       string
}

// bundleRow is one rendered role-bundle line (table/markdown).
type bundleRow struct {
	role        string
	permissions string
	admin       string
}

// aliasJSON / bundleJSON are the machine-readable views. `enum` is the alias
// resolved to the selected scope (omitted when an account-only alias is viewed
// under --scope app); adminConferring marks the all-permissions alias/bundle
// (ADR-0017 §5).
type aliasJSON struct {
	Alias           string   `json:"alias"`
	AccountEnum     string   `json:"accountEnum"`
	AppEnum         string   `json:"appEnum,omitempty"`
	Enum            string   `json:"enum,omitempty"`
	Label           string   `json:"label"`
	Bundles         []string `json:"bundles,omitempty"`
	AdminConferring bool     `json:"adminConferring"`
}

type bundleJSON struct {
	Role            string   `json:"role"`
	Permissions     []string `json:"permissions"`
	Enums           []string `json:"enums"`
	AdminConferring bool     `json:"adminConferring"`
}

type jsonView struct {
	Scope   string       `json:"scope"`
	Aliases []aliasJSON  `json:"aliases"`
	Bundles []bundleJSON `json:"bundles"`
}

// Payload is the offline vocabulary snapshot for the selected scope.
type Payload struct {
	Scope vocab.Scope
}

// Renderers satisfies output.Renderable.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) aliasRows() []aliasRow {
	all := vocab.Aliases()
	rows := make([]aliasRow, 0, len(all))
	for _, a := range all {
		appEnum := appEnumNone
		if e, ok := a.AppEnum(); ok {
			appEnum = e
		}
		rows = append(rows, aliasRow{
			alias:       a.Name,
			accountEnum: a.AccountEnum(),
			appEnum:     appEnum,
			bundles:     strings.Join(vocab.BundlesContaining(a.Name), ","),
			label:       a.Label,
		})
	}
	return rows
}

func (p Payload) bundleRows() []bundleRow {
	all := vocab.Bundles()
	rows := make([]bundleRow, 0, len(all))
	for _, b := range all {
		admin := ""
		if b.IsAdminConferring() {
			admin = "yes"
		}
		rows = append(rows, bundleRow{
			role:        b.Name,
			permissions: strings.Join(b.Members, ","),
			admin:       admin,
		})
	}
	return rows
}

var aliasCols = []output.Column[aliasRow]{
	{Key: "alias", Header: "ALIAS", Value: func(r aliasRow) string { return r.alias }},
	{Key: "account", Header: "ACCOUNT ENUM", Value: func(r aliasRow) string { return r.accountEnum }},
	{Key: "app", Header: "APP ENUM", Value: func(r aliasRow) string { return r.appEnum }},
	{Key: "bundles", Header: "BUNDLES", Value: func(r aliasRow) string { return r.bundles }},
	{Key: "label", Header: "LABEL", Value: func(r aliasRow) string { return r.label }},
}

var bundleCols = []output.Column[bundleRow]{
	{Key: "role", Header: "ROLE", Value: func(r bundleRow) string { return r.role }},
	{Key: "permissions", Header: "PERMISSIONS", Value: func(r bundleRow) string { return r.permissions }},
	{Key: "admin", Header: "ADMIN", Value: func(r bundleRow) string { return r.admin }},
}

func (p Payload) renderTable(w io.Writer) error {
	if _, err := io.WriteString(w, "Permission aliases (scope: "+p.Scope.String()+")\n"); err != nil {
		return err
	}
	if err := output.RenderTable(w, aliasCols, p.aliasRows()); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\nRole bundles\n"); err != nil {
		return err
	}
	return output.RenderTable(w, bundleCols, p.bundleRows())
}

func (p Payload) renderMarkdown(w io.Writer) error {
	if _, err := io.WriteString(w, "## Permission aliases (scope: "+p.Scope.String()+")\n\n"); err != nil {
		return err
	}
	if err := output.RenderMarkdown(w, aliasCols, p.aliasRows()); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n## Role bundles\n\n"); err != nil {
		return err
	}
	return output.RenderMarkdown(w, bundleCols, p.bundleRows())
}

func (p Payload) renderJSON(w io.Writer) error {
	view := jsonView{Scope: p.Scope.String()}
	for _, a := range vocab.Aliases() {
		row := aliasJSON{
			Alias:           a.Name,
			AccountEnum:     a.AccountEnum(),
			Label:           a.Label,
			Bundles:         vocab.BundlesContaining(a.Name),
			AdminConferring: a.IsAdminConferring(),
		}
		if e, ok := a.AppEnum(); ok {
			row.AppEnum = e
		}
		if e, ok := a.Enum(p.Scope); ok {
			row.Enum = e
		}
		view.Aliases = append(view.Aliases, row)
	}
	for _, b := range vocab.Bundles() {
		view.Bundles = append(view.Bundles, bundleJSON{
			Role:            b.Name,
			Permissions:     b.Members,
			Enums:           b.Enums(p.Scope),
			AdminConferring: b.IsAdminConferring(),
		})
	}
	return output.WriteJSON(w, view)
}

// Run is the business function the kernel invokes. It is fully offline: it
// parses --scope and returns the vocabulary snapshot. No Account, no network.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	scope, err := vocab.ParseScope(in.Scope)
	if err != nil {
		return nil, err
	}
	return Payload{Scope: scope}, nil
}

// NewCommand returns the cobra command for `gplay team permissions`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "List gplay's permission aliases and role bundles (offline)",
		Long: `Print gplay's permission vocabulary: the curated aliases (each with its
account-wide ` + "`_GLOBAL`" + ` enum, its per-app enum, the role bundles that
include it, and a label) and the frozen role bundles (viewer, reviewer,
tester-manager, release-manager, admin).

This command is OFFLINE: it makes no API call and needs no credentials. It is
the single source of truth the ` + "`team`" + ` write commands resolve --role and
--permissions through, and the place the "unknown permission" error points.

--scope account|app selects which enum family aliases resolve to (account
` + "`_GLOBAL`" + ` for ` + "`team users`" + `, bare for ` + "`team grants`" + `); --output json marks the
admin-conferring alias and bundle so an agent can discover the --grant-admin
gate before building a command. Any raw CAN_* enum is also always accepted by
the write commands, even one with no alias here.`,
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
	cmd.Flags().StringVar(&in.Scope, "scope", "account", "permission scope to resolve enums for: account or app")
	return cmd
}
