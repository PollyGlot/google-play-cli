// Package listcmd implements `gplay apps list`: print every Android
// package registered under the active Account, marking the one pinned
// by the current repo's .gplay/config.json. The active Account is the
// resolver's choice (rc.AccountName from --account / GPLAY_ACCOUNT /
// cascade), falling back to rc.Resolved.ConfigAccount when no resolver
// layer materialised a credential (read-only listing doesn't need one).
// The registry is read via internal/apps/registry against
// rc.Resolved.Accounts. See Run's doc for the full precedence rules.
package listcmd

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// pinMarker is the glyph the table and markdown renderers print for the
// row matching .gplay/config.json's pin. The check mark renders in every
// modern terminal and is conventional in CLI status output.
const pinMarker = "✓"

// Input has no command-specific flags; everything list needs comes from
// rc.Resolved.
type Input struct{}

// Payload is the rendered data. The kernel calls Renderers() and
// dispatches against the resolved Format.
type Payload struct {
	Apps []AppRow `json:"apps"`
}

// AppRow is one row of the list.
type AppRow struct {
	Package string `json:"package"`
	Pinned  bool   `json:"pinned"`
}

// authError signals "no Account active in the cascade"; ExitCode()=10
// per docs/DESIGN.md §9 (no credential resolved). Mirrors the shape of
// addcmd.authError so exit.For dispatches both the same way.
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
func (e *authError) ExitCode() int { return 10 }

// usageError signals CLI misuse (an inline credential cannot scope the
// registry); ExitCode()=2 per docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Run resolves the Account to list (per the precedence rules below),
// reads its packages from the registry, marks the row matching
// rc.Resolved.Pin (if any), and returns a Renderable payload.
//
// Account precedence — must mirror the resolver's so `--account altname`
// and `GPLAY_ACCOUNT=altname` are honored exactly as for write-side
// commands (this is the bug addcmd.go:65-90 explicitly warns against
// when picking ConfigAccount over AccountName):
//
//  1. rc.AccountName when non-empty — populated by the resolver from
//     --account, GPLAY_ACCOUNT, or the cascade. This is the post-
//     resolution truth and beats ConfigAccount whenever both are set.
//  2. rc.Resolved.ConfigAccount when rc.Account == nil AND
//     rc.AccountName == "" — happens when the keystore could not
//     materialise a credential for the cascade's Active layer (common in
//     tests, also in CI before `auth login`). Listing is read-only so we
//     don't need a working credential to surface packages from the
//     registry — fall back to the cascade name and continue.
//  3. rc.Account != nil with rc.AccountName == "" — the inline-credential
//     case (--service-account / GPLAY_SERVICE_ACCOUNT). Inline creds have
//     no local Account name so the registry cannot be scoped to them; we
//     return a usageError (exit 2) telling the user this limitation.
//  4. Neither name nor credential resolved — authError (exit 10).
//
// Once Account is chosen, we also assert it exists in
// rc.Resolved.Accounts — a stale local override or a logged-out Account
// would otherwise produce the same "no packages" hint as a fresh
// Account, while `apps add` would immediately fail with
// ErrUnknownAccount. Distinct error → distinct user action.
func Run(rc *kernel.RunContext, _ Input) (output.Renderable, error) {
	if rc.Resolved == nil {
		// Defensive: production buildRunContext always populates Resolved,
		// but a hand-built RunContext (e.g. a future test, a partial-init
		// refactor) could leave it nil — fail with a clear authError
		// instead of nil-derefing.
		return nil, &authError{msg: "apps list: no resolved config; run `gplay auth login` to register an Account"}
	}
	account := rc.AccountName
	if account == "" {
		// Empty name ⟹ only an inline or no-source layer is reachable,
		// so resolving the credential here never probes the keyring; the
		// common stored-Account path keeps `apps list` keystore-free.
		if err := rc.EnsureAccount(); err != nil {
			// A malformed inline credential (--service-account /
			// GPLAY_SERVICE_ACCOUNT) fails loudly (exit 10, the real cause)
			// instead of silently falling back to ConfigAccount below and
			// listing another Account's packages (ADR-0020).
			return nil, err
		}
		if rc.Account != nil {
			// Inline credential supplied via --service-account or
			// GPLAY_SERVICE_ACCOUNT: a real credential resolved, but it
			// has no local name so the per-Account registry cannot be
			// scoped to it. Mirror addcmd.go:85-87's clearer message
			// rather than misdirecting the user to `auth login`.
			return nil, &usageError{msg: "apps list: cannot list under an inline credential (--service-account / GPLAY_SERVICE_ACCOUNT); first `gplay auth login` then re-run with --account <name>"}
		}
		// Fall back to the cascade name when no resolver layer chose one.
		// Listing is read-only so a credentialless cascade Account is
		// still useful (registry is local).
		account = rc.Resolved.ConfigAccount
	}
	if account == "" {
		return nil, &authError{msg: "apps list: no active Account; run `gplay auth login` to register one, or pass --account"}
	}
	if !accountInResolved(rc.Resolved.Accounts, account) {
		return nil, &authError{msg: fmt.Sprintf(
			"apps list: Account %q is not in the global config (keystore-only Accounts are unsupported); run `gplay auth login` to register it",
			account,
		)}
	}
	pkgs := registry.List(rc.Resolved.Accounts, account)
	// Empty registry: return an empty Payload and let the renderer own
	// the empty-state UX. Previously a stderr hint here duplicated the
	// markdown renderer's own placeholder (`--output markdown 2>&1` saw
	// the message twice); pushing the placeholder into each renderer
	// keeps stderr quiet and gives every format its own contextual notice.
	rows := make([]AppRow, 0, len(pkgs))
	for _, p := range pkgs {
		rows = append(rows, AppRow{Package: p, Pinned: p == rc.Resolved.Pin})
	}
	return Payload{Apps: rows}, nil
}

// accountInResolved reports whether name is registered in the global
// config's Accounts slice. Mirror of addcmd's accountInGlobal — kept
// private to listcmd because the cascade snapshot lives on
// rc.Resolved.Accounts here rather than a freshly-loaded *config.Global.
func accountInResolved(accounts []config.Account, name string) bool {
	for _, a := range accounts {
		if a.Name == name {
			return true
		}
	}
	return false
}

// Renderers satisfies output.Renderable.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Apps) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Apps) },
	}
}

// emptyHint is the user-facing line every renderer emits for an empty
// registry. Keeping it in one place stops the table and markdown
// placeholders from drifting apart.
const emptyHint = "no packages registered. Run `gplay apps add <package>` to register one"

// renderTable writes the Package / Pinned table via tabwriter. An empty
// list collapses to a single parenthetical line on stdout (matching
// commands/auth/list/list.go's UX); the JSON renderer keeps the strict
// `{"apps":[]}` shape so machine consumers stay clean.
func renderTable(w io.Writer, rows []AppRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "(%s.)\n", emptyHint)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PACKAGE\tPINNED"); err != nil {
		return err
	}
	for _, r := range rows {
		mark := ""
		if r.Pinned {
			mark = pinMarker
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", r.Package, mark); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// renderMarkdown emits a GFM table with one row per AppRow, marking the
// pinned row with the same glyph used by the table renderer. An empty
// list collapses to a single italic paragraph carrying the same
// emptyHint as the table path.
func renderMarkdown(w io.Writer, rows []AppRow) error {
	if len(rows) == 0 {
		// Capitalise the first letter for prose-style markdown.
		_, err := fmt.Fprintf(w, "_No packages registered. Run `gplay apps add <package>` to register one._\n")
		return err
	}
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		mark := ""
		if r.Pinned {
			mark = pinMarker
		}
		cells = append(cells, []string{r.Package, mark})
	}
	return output.MarkdownTable(w, []string{"Package", "Pinned"}, cells)
}

// NewCommand returns the cobra command for `gplay apps list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var outputFlag string
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List packages registered under the active Account",
		Long:          `List every Android package registered under the active Account in gplay's local registry. In table and markdown output the row matching the current repo's .gplay/config.json pin (if any) is marked with a ✓ in the Pinned column; in JSON output the same row carries "pinned": true. Pass no positional arguments — listing scope is always the active Account.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{})
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	return cmd
}
