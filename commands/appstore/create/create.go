package create

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

type Input struct {
	StorePackage string
	Package      string
	DryRun       bool
}

type Payload struct {
	StorePackage string
	Package      string
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
		if _, err := fmt.Fprintf(w, "would create hosted app %s in app store %s (dry-run)\n", p.Package, p.StorePackage); err != nil {
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
	if _, err := fmt.Fprintf(w, "## appstore create%s\n\n", suffix); err != nil {
		return err
	}
	rows := make([][]string, 0, 3)
	if p.DryRun {
		rows = append(rows, []string{"ACTION", "would create"})
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
	}
}

type createdView struct {
	OK                  bool   `json:"ok"`
	AppStorePackageName string `json:"appStorePackageName"`
	PackageName         string `json:"packageName"`
}

type dryRunView struct {
	DryRun              bool     `json:"dryRun"`
	AppStorePackageName string   `json:"appStorePackageName"`
	PackageName         string   `json:"packageName"`
	Requires            []string `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, AppStorePackageName: p.StorePackage, PackageName: p.Package, Requires: []string{}})
	}
	return output.WriteAckOrRaw(w, p.Raw, createdView{OK: true, AppStorePackageName: p.StorePackage, PackageName: p.Package})
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	storePackage, err := appstorecmd.ResolveStorePackage(in.StorePackage)
	if err != nil {
		return nil, err
	}
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	if in.DryRun {
		return Payload{StorePackage: storePackage, Package: pkg, DryRun: true}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	raw, err := appstore.CreateHostedApp(rc.Ctx, httpClient, storePackage, pkg)
	if err != nil {
		return nil, appstorecmd.ClassifyReview(storePackage, err)
	}

	rc.Confirmf("created hosted app %s in app store %s", pkg, storePackage)
	return Payload{StorePackage: storePackage, Package: pkg, Raw: raw}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create the hosted app record a third-party app store needs before any other call",
		Long: `Create the hosted app record for an app a third-party Android app store hosts,
so the store can submit it to Google for review.

This is the FIRST call for any hosted app: Google requires it before any other
request for that app, so nothing else in the appstore surface (metadata,
artifacts, images, policy declarations, publish status) works until it has
succeeded.

Two identifiers meet here, and mixing them up is the common mistake:

  --store-package  the app store's OWN package name (the caller: the
                   third-party store enrolled for alternative distribution),
                   falling back to $` + appstorecmd.EnvStorePackage + `
  --package        the hosted app's package name (the subject), defaulting to
                   the repo's .gplay/config.json pin when omitted

The call is Edit-free: it opens no Edit and joins none. The API exposes
NO delete for the record: once created it cannot be removed (there is no
delete verb to look for), only its publish status can change. Creating the record
twice is rejected by the API as a conflict (exit 60), so the command is safe to
retry but not to re-run blindly.

The response carries no fields (the acknowledgement IS the result), so the
human views echo the two identifiers; --output json passes the API response
through verbatim, falling back to a gplay-shaped success object only
when the API answers with no body at all. Use --dry-run to preview the resolved
target with no HTTP call. GPLAY_READONLY refuses the write (exit 4) but lets
--dry-run run. A 403 names the app store enrollment the call requires.`,
		Example: `  # Create the record for a hosted app (the first call, once per app)
  gplay appstore create --store-package com.example.store --package com.example.game

  # With the store set once for the shell, preview without any HTTP call
  export GPLAY_APP_STORE_PACKAGE=com.example.store
  gplay appstore create --package com.example.game --dry-run`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	appstorecmd.RegisterStorePackageFlag(cmd, &in.StorePackage)
	cmd.Flags().StringVar(&in.Package, "package", "", "package name of the hosted app (overrides .gplay/config.json pin)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "preview the resolved target without any HTTP call")
	return cmd
}
