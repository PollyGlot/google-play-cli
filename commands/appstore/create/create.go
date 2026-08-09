// Package create implements `gplay appstore create --store-package <sp>
// [--package <pkg>]`: create the hosted app record a third-party Android app
// store needs before it can do anything else with an app it hosts, via
// `appstoreappsreview.createappstorehostedapp`.
//
// It is the entry point of the whole `appstore` surface — Google's own method
// description is "This must be called before any other RPCs for this hosted
// app", so every later gesture (metadata update, APK upload, image upload,
// policy declaration file, publish status) presupposes a record this command
// created. Two addressing values meet here: --store-package names the calling
// app store (the `appstore/{appStorePackageName}` path key), --package names
// the hosted app (the request body), defaulting to the project pin.
//
// MarkMutating so GPLAY_READONLY refuses it (exit 4); --dry-run previews the
// resolved target without any HTTP call. Edit-free — the call is not under
// `/edits/`. Ships [experimental] (ADR-0010/ADR-0042): a brand-new namespace on
// a third addressing axis, none of it exercised against a real enrolled app
// store yet.
package create

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

// Input is the request-shaped struct cobra builds from the flags.
type Input struct {
	StorePackage string
	Package      string
	DryRun       bool
}

// Payload renders the created hosted app record (or, on --dry-run, the record
// that would be created). Raw carries the verbatim API body for the ADR-0003
// --output json pass-through; CreateAppStoreHostedAppResponse models no fields,
// so the human views describe what was created from the resolved inputs rather
// than from the response.
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

// renderTable writes the two identifying values as `FIELD<TAB>VALUE` lines
// (like `apps view` / `orders view`) — the record has no server-assigned id to
// lead with. A dry-run leads with the rehearsed action instead.
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

// rows is the identifying pair shared by the human views, in a fixed order.
func (p Payload) rows() [][2]string {
	return [][2]string{
		{"APP_STORE_PACKAGE_NAME", p.StorePackage},
		{"PACKAGE_NAME", p.Package},
	}
}

// createdView is the gplay-shaped success object emitted only when the API
// returns no body at all (see renderJSON).
type createdView struct {
	OK                  bool   `json:"ok"`
	AppStorePackageName string `json:"appStorePackageName"`
	PackageName         string `json:"packageName"`
}

// dryRunView is the gplay-shaped --dry-run JSON: the resolved target plus the
// machine-readable `requires` array (ADR-0017 §4) — empty, because create needs
// no safety flag beyond a writable environment.
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
	// ADR-0003: the API response is passed through verbatim. The documented
	// exception applies only when there is nothing to pass through —
	// CreateAppStoreHostedAppResponse models no fields, so a server that
	// answers with an empty body (rather than `{}`) would leave --output json,
	// the CI default, with zero bytes to parse. As in `orders refund` and
	// `team users remove`, a gplay-shaped success object stands in.
	if len(bytes.TrimSpace(p.Raw)) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, createdView{OK: true, AppStorePackageName: p.StorePackage, PackageName: p.Package})
}

// Run is the business function the kernel invokes. It resolves the app store
// package name and the hosted app's package, then — unless --dry-run
// short-circuits before any network — issues the create call.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	storePackage, err := appstorecmd.ResolveStorePackage(in.StorePackage)
	if err != nil {
		return nil, err
	}
	pkg, err := appstorecmd.ResolvePackage(rc, in.Package)
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

	// DESIGN §8: a committed mutation prints one ✓ line on stderr; stdout stays
	// data-only.
	rc.Confirmf("created hosted app %s in app store %s", pkg, storePackage)
	return Payload{StorePackage: storePackage, Package: pkg, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay appstore create`.
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
request for that app, so nothing else in the appstore surface — metadata,
artifacts, images, policy declarations, publish status — works until it has
succeeded.

Two identifiers meet here, and mixing them up is the common mistake:

  --store-package  the app store's OWN package name (the caller — the
                   third-party store enrolled for alternative distribution),
                   falling back to $` + appstorecmd.EnvStorePackage + ` (ADR-0043)
  --package        the hosted app's package name (the subject), defaulting to
                   the repo's .gplay/config.json pin when omitted

The call is Edit-free — it opens no Edit and joins none. The API exposes
NO delete for the record: once created it cannot be removed (there is no
delete verb to look for), only its publish status can change. Creating the record
twice is rejected by the API as a conflict (exit 60), so the command is safe to
retry but not to re-run blindly.

The response carries no fields (the acknowledgement IS the result), so the
human views echo the two identifiers; --output json passes the API response
through verbatim (ADR-0003), falling back to a gplay-shaped success object only
when the API answers with no body at all. Use --dry-run to preview the resolved
target with no HTTP call. GPLAY_READONLY refuses the write (exit 4) but lets
--dry-run run. A 403 names the app store enrollment the call requires.`,
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
