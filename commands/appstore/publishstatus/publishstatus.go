// Package publishstatus implements `gplay appstore publish-status
// --store-package <sp> [--package <pkg>] <published|unpublished>`: flip a hosted
// app between visible and withdrawn in a third-party Android app store, via
// `appstoreappsreview.updateAppStoreHostedAppPublishStatus`.
//
// Google documents the state as already PUBLISHED after
// `UpdateAppStoreHostedApp` ("It is not necessary to call this RPC explicitly to
// set an app to PUBLISHED"), so in practice this command exists to pull an app
// back OUT of the store, and to put it back. That symmetry is the whole design:
// the operation is reversible in both directions by a single call, which is why
// it carries no --confirm gate (ADR-0043 gates what is irreversible *and*
// externally visible; this is only the latter). --dry-run still previews the
// resolved target with no HTTP call.
//
// The CLI speaks the two human words `published` / `unpublished`,
// case-insensitively, and maps them onto the API's
// APP_STORE_APP_PUBLISH_STATE_* enum. An unknown word is CLI misuse (exit 2)
// that enumerates the accepted values: a server-side 400 for a typo would cost a
// round trip and say less. The third enum member,
// APP_STORE_APP_PUBLISH_STATE_UNSPECIFIED, is documented "Do not use" and is
// therefore deliberately not reachable from the CLI.
//
// Edit-free: the call is not under `/edits/`.
package publishstatus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

// The human words the CLI exposes, in the order --help and the usage error list
// them. Spelled once so the error message can never drift from what
// resolveState actually accepts.
const (
	statePublished   = "published"
	stateUnpublished = "unpublished"
)

// Input is the request-shaped struct cobra builds from the flags and the single
// positional argument.
type Input struct {
	StorePackage string
	Package      string
	// State is the raw positional word as typed; resolveState normalizes and
	// validates it, so Run stays the only place that knows the enum.
	State  string
	DryRun bool
}

// resolveState maps the human word onto the API enum. The comparison is
// case-insensitive (an agent may well emit `PUBLISHED`), and anything else is a
// usage error naming both accepted values: never a 400 from the server.
func resolveState(word string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case statePublished:
		return appstore.PublishStatePublished, nil
	case stateUnpublished:
		return appstore.PublishStateUnpublished, nil
	case "":
		return "", appstorecmd.Usagef("missing publish state: gplay appstore publish-status <%s|%s>", statePublished, stateUnpublished)
	default:
		return "", appstorecmd.Usagef("invalid publish state %q: accepted values are %s and %s", strings.TrimSpace(word), statePublished, stateUnpublished)
	}
}

// Payload renders the applied publish state (or, on --dry-run, the state that
// would be applied). Raw carries the verbatim API body for the ADR-0003
// --output json pass-through; UpdateAppStoreHostedAppPublishStatusResponse
// models no fields, so the human views describe the change from the resolved
// inputs rather than from the response.
type Payload struct {
	StorePackage string
	Package      string
	// Word is the human spelling (`published` / `unpublished`), State the API
	// enum actually sent. Both are shown: the word is what the operator typed,
	// the enum is what a support ticket or an API log will quote.
	Word   string
	State  string
	DryRun bool
	Raw    json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

// renderTable writes the identifying values as `FIELD<TAB>VALUE` lines (like
// `appstore create`): the call returns no server-assigned field to lead with.
// A dry-run leads with the rehearsed action instead.
func (p Payload) renderTable(w io.Writer) error {
	if p.DryRun {
		if _, err := fmt.Fprintf(w, "would set hosted app %s to %s in app store %s (dry-run)\n", p.Package, p.Word, p.StorePackage); err != nil {
			return err
		}
		return nil
	}
	for _, row := range p.rows() {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", row[0], row[1]); err != nil {
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
	if _, err := fmt.Fprintf(w, "## appstore publish-status%s\n\n", suffix); err != nil {
		return err
	}
	rows := make([][]string, 0, 4)
	if p.DryRun {
		rows = append(rows, []string{"ACTION", "would set publish state"})
	}
	for _, r := range p.rows() {
		rows = append(rows, []string{r[0], r[1]})
	}
	return output.MarkdownTable(w, []string{"FIELD", "VALUE"}, rows)
}

// rows is the identifying triple shared by the human views, in a fixed order:
// the two addressing values then the state that was applied.
func (p Payload) rows() [][2]string {
	return [][2]string{
		{"APP_STORE_PACKAGE_NAME", p.StorePackage},
		{"PACKAGE_NAME", p.Package},
		{"PUBLISH_STATE", p.State},
	}
}

// updatedView is the gplay-shaped success object emitted only when the API
// returns no body at all (see renderJSON).
type updatedView struct {
	OK                  bool   `json:"ok"`
	AppStorePackageName string `json:"appStorePackageName"`
	PackageName         string `json:"packageName"`
	PublishState        string `json:"publishState"`
}

// dryRunView is the gplay-shaped --dry-run JSON: the resolved target and state,
// plus the machine-readable `requires` array (ADR-0017 §4): empty, because
// flipping the publish status is reversible and needs no safety flag beyond a
// writable environment.
type dryRunView struct {
	DryRun              bool     `json:"dryRun"`
	AppStorePackageName string   `json:"appStorePackageName"`
	PackageName         string   `json:"packageName"`
	PublishState        string   `json:"publishState"`
	Requires            []string `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{
			DryRun:              true,
			AppStorePackageName: p.StorePackage,
			PackageName:         p.Package,
			PublishState:        p.State,
			Requires:            []string{},
		})
	}
	// ADR-0003: the API response is passed through verbatim. The documented
	// exception applies only when there is nothing to pass through:
	// UpdateAppStoreHostedAppPublishStatusResponse models no fields, so a server
	// answering with an empty body (rather than `{}`) would leave --output json,
	// the CI default, with zero bytes to parse. As in `appstore create`, a
	// gplay-shaped success object stands in.
	if len(bytes.TrimSpace(p.Raw)) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, updatedView{OK: true, AppStorePackageName: p.StorePackage, PackageName: p.Package, PublishState: p.State})
}

// Run is the business function the kernel invokes. It validates the requested
// state and resolves both addressing values (all exit 2), then, unless
// --dry-run short-circuits before any network: issues the publish-status call.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	// The positional word is validated first: it is the argument the command is
	// about, and a typo there is the misuse most worth naming precisely.
	state, err := resolveState(in.State)
	if err != nil {
		return nil, err
	}
	storePackage, err := appstorecmd.ResolveStorePackage(in.StorePackage)
	if err != nil {
		return nil, err
	}
	pkg, err := appstorecmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	word := strings.ToLower(strings.TrimSpace(in.State))

	if in.DryRun {
		return Payload{StorePackage: storePackage, Package: pkg, Word: word, State: state, DryRun: true}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	raw, err := appstore.UpdatePublishStatus(rc.Ctx, httpClient, storePackage, pkg, state)
	if err != nil {
		return nil, appstorecmd.ClassifyHostedApp(storePackage, pkg, err)
	}

	// DESIGN §8: a committed mutation prints one ✓ line on stderr; stdout stays
	// data-only.
	rc.Confirmf("set hosted app %s to %s in app store %s", pkg, word, storePackage)
	return Payload{StorePackage: storePackage, Package: pkg, Word: word, State: state, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay appstore publish-status`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "publish-status <" + statePublished + "|" + stateUnpublished + ">",
		Short: "Publish or unpublish a hosted app in a third-party app store (reversible)",
		Long: `Set the publish status of a hosted app in a third-party Android app store:
` + statePublished + ` makes it available in that store, ` + stateUnpublished + ` withdraws it.

The state is a positional argument and is matched case-insensitively; any other
value is refused as CLI misuse (exit 2) before any HTTP call. It maps onto the
API's APP_STORE_APP_PUBLISH_STATE_PUBLISHED / _UNPUBLISHED enum.

Google documents the state as ALREADY PUBLISHED after a successful
` + "`gplay appstore update`" + `: it is not necessary to call this to publish a freshly
updated app. In practice this command serves to take an app back OUT of the
store, and to put it back in later.

The change is REVERSIBLE IN BOTH DIRECTIONS: one call withdraws the app, one
call restores it, so no --confirm gate applies (contrast ` + "`gplay appstore update`" + `,
which submits to review irreversibly). Unpublishing is still immediately visible
to that store's users, so --dry-run previews the resolved target and state with
no HTTP call.

Two identifiers meet here, as everywhere in this namespace:

  --store-package  the app store's OWN package name (the caller: the
                   third-party store enrolled for alternative distribution),
                   falling back to $` + appstorecmd.EnvStorePackage + ` (ADR-0043)
  --package        the hosted app's package name (the subject), defaulting to
                   the repo's .gplay/config.json pin when omitted

The record must already exist: run ` + "`gplay appstore create`" + ` first, or the call is
rejected. The call is Edit-free: it opens no Edit and joins none.

The response carries no fields (the acknowledgement IS the result), so the human
views echo the identifiers and the applied state; --output json passes the API
response through verbatim (ADR-0003), falling back to a gplay-shaped success
object only when the API answers with no body at all. GPLAY_READONLY refuses the
write (exit 4) but lets --dry-run run. A 403 names the app store enrollment the
call requires.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.State = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	appstorecmd.RegisterStorePackageFlag(cmd, &in.StorePackage)
	cmd.Flags().StringVar(&in.Package, "package", "", "package name of the hosted app (overrides .gplay/config.json pin)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "preview the resolved target and state without any HTTP call")
	return cmd
}
