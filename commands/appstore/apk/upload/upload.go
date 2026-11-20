package upload

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	"github.com/PollyGlot/google-play-cli/internal/artifact"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

const citeHint = "cite this apkId in `gplay appstore submit` as activeApks.activeApkSets[].baseApkId (or .splitApkId[]): the APK is distributed by that call, not by this one"

type Input struct {
	StorePackage  string
	Package       string
	Path          string
	SkipPreflight bool
	DryRun        bool
}

type Payload struct {
	StorePackage string
	Package      string
	Path         string
	APKID        string
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

func (p Payload) rows() [][2]string {
	return [][2]string{
		{"APK_ID", p.APKID},
		{"APP_STORE_PACKAGE_NAME", p.StorePackage},
		{"PACKAGE_NAME", p.Package},
		{"FILE", p.Path},
	}
}

func (p Payload) renderTable(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "would upload APK %s for hosted app %s in app store %s (dry-run)\n", p.Path, p.Package, p.StorePackage)
		return err
	}
	for _, row := range p.rows() {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\n%s\n", citeHint)
	return err
}

func (p Payload) renderMarkdown(w io.Writer) error {
	suffix := ""
	if p.DryRun {
		suffix = " (dry-run)"
	}
	if _, err := fmt.Fprintf(w, "## appstore apk upload%s\n\n", suffix); err != nil {
		return err
	}
	rows := make([][]string, 0, len(p.rows())+1)
	if p.DryRun {
		rows = append(rows, []string{"ACTION", "would upload"})
	}
	for _, r := range p.rows() {
		rows = append(rows, []string{r[0], r[1]})
	}
	if err := output.MarkdownTable(w, []string{"FIELD", "VALUE"}, rows); err != nil {
		return err
	}
	if p.DryRun {
		return nil
	}
	_, err := fmt.Fprintf(w, "\n> **hint:** %s\n", citeHint)
	return err
}

type dryRunView struct {
	DryRun              bool     `json:"dryRun"`
	AppStorePackageName string   `json:"appStorePackageName"`
	PackageName         string   `json:"packageName"`
	File                string   `json:"file"`
	Requires            []string `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, AppStorePackageName: p.StorePackage, PackageName: p.Package, File: p.Path, Requires: []string{}})
	}
	_, err := w.Write(p.Raw)
	return err
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return nil, exit.Usagef("missing APK path: gplay appstore apk upload --%s <store-pkg> <file.apk>", appstorecmd.FlagStorePackage)
	}
	storePackage, err := appstorecmd.ResolveStorePackage(in.StorePackage)
	if err != nil {
		return nil, err
	}
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	if in.DryRun {
		return Payload{StorePackage: storePackage, Package: pkg, Path: path, DryRun: true}, nil
	}

	if err := artifact.Verify(rc.Stderr, path, artifact.Expect{
		Kinds:   []artifact.Kind{artifact.KindAPK},
		Package: pkg,
	}, artifact.Options{Skip: in.SkipPreflight}); err != nil {
		return nil, err
	}

	httpClient, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}

	apkID, raw, err := appstore.UploadAPK(rc.Ctx, httpClient, storePackage, pkg, path)
	if err != nil {
		return nil, appstorecmd.ClassifyHostedApp(storePackage, pkg, err)
	}

	rc.Confirmf("uploaded APK %s for hosted app %s in app store %s: apkId %s; %s", path, pkg, storePackage, apkID, citeHint)
	return Payload{StorePackage: storePackage, Package: pkg, Path: path, APKID: apkID, Raw: raw}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "upload <file.apk>",
		Short: "Upload an APK for a hosted app and print the apkId to cite in appstore submit",
		Long: `Upload an APK for an app a third-party Android app store hosts, and print the
tracking id (apkId) Google assigns it.

The id is the point of the call. Nothing is distributed here: the uploaded APK
sits inert until a later ` + "`gplay appstore submit`" + ` names its id in
activeApks.activeApkSets[].baseApkId (the base APK) or .splitApkId[] (the
splits). Upload the binary once, then cite the id: re-sending gigabytes on
every metadata change is never necessary.

Two identifiers address the call, and mixing them up is the common mistake:

  --store-package  the app store's OWN package name (the caller: the
                   third-party store enrolled for alternative distribution),
                   falling back to $` + appstorecmd.EnvStorePackage + `
  --package        the hosted app's package name (the subject), defaulting to
                   the repo's .gplay/config.json pin when omitted

The API accepts up to 10 GiB per APK and the upload announces
application/octet-stream. The transfer is resumable, so a transient network
failure re-sends from the last byte the server acknowledged rather than
restarting. The command is exempt from the 60s control-plane timeout; pass the
global --timeout to bound it explicitly.

The hosted app record must already exist: run ` + "`gplay appstore create`" + ` first.
The call is Edit-free: it opens no Edit and joins none.

No --confirm is required: an upload is inert (it produces an id and changes
nothing a user can see): gplay gates only writes that are both
irreversible AND externally visible. GPLAY_READONLY still refuses it (exit 4)
but lets --dry-run run.

--output json passes the API response through verbatim. --dry-run
previews the resolved target with no HTTP call and without opening the file, so
it works before the artifact is built. An unreadable path is a client-side
failure (exit 20); a 403 names the app store enrollment the call requires.`,
		Example: `  # Upload the base APK and keep the apkId for gplay appstore submit
  gplay appstore apk upload game-base.apk --store-package com.example.store --package com.example.game --output json

  # Preview the target before the APK is even built
  gplay appstore apk upload game-base.apk --store-package com.example.store --package com.example.game --dry-run`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.Path = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	appstorecmd.RegisterStorePackageFlag(cmd, &in.StorePackage)
	cmd.Flags().StringVar(&in.Package, "package", "", "package name of the hosted app (overrides .gplay/config.json pin)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "preview the resolved target without any HTTP call or file read")
	cmd.Flags().BoolVar(&in.SkipPreflight, "skip-preflight", false, "skip the local artifact check (that the file is an APK declaring this package) and upload it as-is")
	return cmd
}
