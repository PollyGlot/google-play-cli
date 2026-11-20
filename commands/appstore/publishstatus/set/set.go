package set

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

const (
	statePublished   = "published"
	stateUnpublished = "unpublished"
)

type Input struct {
	StorePackage string
	Package      string
	State        string
	DryRun       bool
}

func resolveState(word string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case statePublished:
		return appstore.PublishStatePublished, nil
	case stateUnpublished:
		return appstore.PublishStateUnpublished, nil
	case "":
		return "", exit.Usagef("missing publish state: gplay appstore publish-status set <%s|%s>", statePublished, stateUnpublished)
	default:
		return "", exit.Usagef("invalid publish state %q: accepted values are %s and %s", strings.TrimSpace(word), statePublished, stateUnpublished)
	}
}

type Payload struct {
	StorePackage string
	Package      string
	Word         string
	State        string
	DryRun       bool
	Raw          json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

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
	if _, err := fmt.Fprintf(w, "## appstore publish-status set%s\n\n", suffix); err != nil {
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

func (p Payload) rows() [][2]string {
	return [][2]string{
		{"APP_STORE_PACKAGE_NAME", p.StorePackage},
		{"PACKAGE_NAME", p.Package},
		{"PUBLISH_STATE", p.State},
	}
}

type updatedView struct {
	OK                  bool   `json:"ok"`
	AppStorePackageName string `json:"appStorePackageName"`
	PackageName         string `json:"packageName"`
	PublishState        string `json:"publishState"`
}

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
	if len(bytes.TrimSpace(p.Raw)) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, updatedView{OK: true, AppStorePackageName: p.StorePackage, PackageName: p.Package, PublishState: p.State})
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	state, err := resolveState(in.State)
	if err != nil {
		return nil, err
	}
	storePackage, err := appstorecmd.ResolveStorePackage(in.StorePackage)
	if err != nil {
		return nil, err
	}
	pkg, err := rc.Package(in.Package)
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

	rc.Confirmf("set hosted app %s to %s in app store %s", pkg, word, storePackage)
	return Payload{StorePackage: storePackage, Package: pkg, Word: word, State: state, Raw: raw}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "set <" + statePublished + "|" + stateUnpublished + ">",
		Short: "Set a hosted app published or unpublished in a third-party app store (reversible)",
		Long: `Set the publish status of a hosted app in a third-party Android app store:
` + statePublished + ` makes it available in that store, ` + stateUnpublished + ` withdraws it.

The state is a positional argument and is matched case-insensitively; any other
value is refused as CLI misuse (exit 2) before any HTTP call. It maps onto the
API's APP_STORE_APP_PUBLISH_STATE_PUBLISHED / _UNPUBLISHED enum.

Google documents the state as ALREADY PUBLISHED after a successful
` + "`gplay appstore submit`" + `: it is not necessary to call this to publish a freshly
submitted app. In practice this command serves to take an app back OUT of the
store, and to put it back in later.

The change is REVERSIBLE IN BOTH DIRECTIONS: one call withdraws the app, one
call restores it, so no --confirm gate applies (contrast ` + "`gplay appstore submit`" + `,
which submits to review irreversibly). Unpublishing is still immediately visible
to that store's users, so --dry-run previews the resolved target and state with
no HTTP call.

Two identifiers meet here, as everywhere in this namespace:

  --store-package  the app store's OWN package name (the caller: the
                   third-party store enrolled for alternative distribution),
                   falling back to $` + appstorecmd.EnvStorePackage + `
  --package        the hosted app's package name (the subject), defaulting to
                   the repo's .gplay/config.json pin when omitted

The record must already exist: run ` + "`gplay appstore create`" + ` first, or the call is
rejected. The call is Edit-free: it opens no Edit and joins none.

The response carries no fields (the acknowledgement IS the result), so the human
views echo the identifiers and the applied state; --output json passes the API
response through verbatim, falling back to a gplay-shaped success
object only when the API answers with no body at all. GPLAY_READONLY refuses the
write (exit 4) but lets --dry-run run. A 403 names the app store enrollment the
call requires.`,
		Example: `  # Withdraw a hosted app from the store (reversible)
  gplay appstore publish-status set unpublished --store-package com.example.store --package com.example.game

  # Preview putting it back, without any HTTP call
  gplay appstore publish-status set published --store-package com.example.store --package com.example.game --dry-run`,
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
