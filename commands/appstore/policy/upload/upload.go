package upload

import (
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

const citeHint = "cite this fileId in `gplay appstore submit` inside the policy response's documentResponse: the document backs a declaration only once that call names it"

type Input struct {
	StorePackage string
	Package      string
	Path         string
	DryRun       bool
}

type Payload struct {
	StorePackage string
	Package      string
	Path         string
	FileID       string
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
		{"FILE_ID", p.FileID},
		{"FILE_TYPE", appstore.DeclarationFileTypeDocument},
		{"APP_STORE_PACKAGE_NAME", p.StorePackage},
		{"PACKAGE_NAME", p.Package},
		{"FILE", p.Path},
	}
}

func (p Payload) renderTable(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "would upload policy declaration file %s for hosted app %s in app store %s (dry-run)\n", p.Path, p.Package, p.StorePackage)
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
	if _, err := fmt.Fprintf(w, "## appstore policy upload%s\n\n", suffix); err != nil {
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
	FileType            string   `json:"fileType"`
	Requires            []string `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{
			DryRun:              true,
			AppStorePackageName: p.StorePackage,
			PackageName:         p.Package,
			File:                p.Path,
			FileType:            appstore.DeclarationFileTypeDocument,
			Requires:            []string{},
		})
	}
	_, err := w.Write(p.Raw)
	return err
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return nil, exit.Usagef("missing policy declaration file path: gplay appstore policy upload --%s <store-pkg> <file>", appstorecmd.FlagStorePackage)
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

	httpClient, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}

	fileID, raw, err := appstore.UploadPolicyDeclarationFile(rc.Ctx, httpClient, storePackage, pkg, path)
	if err != nil {
		return nil, appstorecmd.ClassifyHostedApp(storePackage, pkg, err)
	}

	rc.Confirmf("uploaded policy declaration file %s for hosted app %s in app store %s: fileId %s; %s", path, pkg, storePackage, fileID, citeHint)
	return Payload{StorePackage: storePackage, Package: pkg, Path: path, FileID: fileID, Raw: raw}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "upload <file>",
		Short: "Upload a policy declaration supporting document and print the fileId to cite in appstore submit",
		Long: `Upload a supporting document for a hosted app's policy declarations, and print
the tracking id (fileId) Google assigns it.

The id is the point of the call. Nothing is declared here: the uploaded
document sits inert until a later ` + "`gplay appstore submit`" + ` names its id inside a
policy response's documentResponse, which is how a declaration answer points at
the evidence backing it.

Two identifiers address the call, and mixing them up is the common mistake:

  --store-package  the app store's OWN package name (the caller: the
                   third-party store enrolled for alternative distribution),
                   falling back to $` + appstorecmd.EnvStorePackage + `
  --package        the hosted app's package name (the subject), defaulting to
                   the repo's .gplay/config.json pin when omitted

The API accepts up to 10 MiB per document, as PDF, JPEG or PNG; the type is
sniffed from the file's leading bytes rather than its extension. The request
declares fileType=` + appstore.DeclarationFileTypeDocument + `, the enum's only
meaningful value, so there is no flag to set. The transfer is resumable.

The hosted app record must already exist: run ` + "`gplay appstore create`" + ` first.
The call is Edit-free: it opens no Edit and joins none.

No --confirm is required: an upload is inert (it produces an id and changes
nothing a user can see): gplay gates only writes that are both
irreversible AND externally visible. GPLAY_READONLY still refuses it (exit 4)
but lets --dry-run run.

--output json passes the API response through verbatim. --dry-run
previews the resolved target with no HTTP call and without opening the file. An
unreadable path is a client-side failure (exit 20); a 403 names the app store
enrollment the call requires.`,
		Example: `  gplay appstore policy upload age-rating-evidence.pdf --store-package com.example.store --package com.example.game
  gplay appstore policy upload age-rating-evidence.pdf --store-package com.example.store --package com.example.game --dry-run`,
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
	return cmd
}
