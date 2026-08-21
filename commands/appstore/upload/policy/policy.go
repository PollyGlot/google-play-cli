// Package policy implements `gplay appstore upload policy --store-package <sp>
// [--package <pkg>] <file>`: stream a policy declaration supporting document
// for a hosted app to
// `appstoreappsreview.uploadAppStoreAppPolicyDeclarationFile` and print the
// tracking id Google assigns it.
//
// The id (not the transfer) is the product of the call. `gplay appstore
// update` cites it inside a policy response's `documentResponse`, which is how
// a declaration answer points at the evidence backing it. Nothing is declared
// by this command: an uploaded document is inert until an `appstore update`
// names its id.
//
// The method is the one upload in the namespace that carries a request body:
// `fileType` is required, and its only meaningful value
// (DECLARATION_FILE_TYPE_DOCUMENT) is sent for us by internal/play/appstore:
// the enum's other member is the UNSPECIFIED zero value, so there is no choice
// to expose as a flag.
//
// MarkMutating at registration so GPLAY_READONLY refuses it (exit 4); --dry-run
// previews the resolved target without any HTTP call AND without opening the
// file. Edit-free: the call is not under `/edits/`. No confirmation gate
// (ADR-0043): a gate is for the irreversible *and* externally visible, and an
// upload is neither: it mints an id and changes nothing a user can see.
package policy

import (
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

// citeHint is the one thing a caller must know after the upload succeeds: the
// id is worthless unless it reaches `appstore update`. Spelled once, shared by
// the human views and the stderr confirmation, so the three never drift.
const citeHint = "cite this fileId in `gplay appstore update` inside the policy response's documentResponse: the document backs a declaration only once that call names it"

// Input is the request-shaped struct cobra builds from the flags plus the
// positional file path.
type Input struct {
	StorePackage string
	Package      string
	Path         string
	DryRun       bool
}

// Payload renders the uploaded document's tracking id (or, on --dry-run, the
// upload that would happen). Raw carries the verbatim API body for the ADR-0003
// --output json pass-through; FileID is the same body's `fileId`, lifted out so
// the human views can lead with it.
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

// rows is the identifying set shared by the human views, in a fixed order with
// FILE_ID first: it is the only value the caller has to carry forward.
// FILE_TYPE is echoed because it travelled on the wire without the caller
// choosing it.
func (p Payload) rows() [][2]string {
	return [][2]string{
		{"FILE_ID", p.FileID},
		{"FILE_TYPE", appstore.DeclarationFileTypeDocument},
		{"APP_STORE_PACKAGE_NAME", p.StorePackage},
		{"PACKAGE_NAME", p.Package},
		{"FILE", p.Path},
	}
}

// renderTable writes `FIELD<TAB>VALUE` lines (like `apps view` / `orders
// view`), then the cite hint: the id alone does not tell an operator where it
// is meant to go. A dry-run leads with the rehearsed action instead.
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
	if _, err := fmt.Fprintf(w, "## appstore upload policy%s\n\n", suffix); err != nil {
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

// dryRunView is the gplay-shaped --dry-run JSON: the resolved target plus the
// machine-readable `requires` array (ADR-0017 §4): empty, because an upload
// needs no safety flag beyond a writable environment. fileType is echoed
// because the request body carries it whether or not the caller asked.
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
	// ADR-0003: the API response is passed through verbatim: it is where the
	// tracking id comes from in the first place. There is no empty-body
	// fallback here, unlike the acknowledgement-only verbs in this namespace:
	// an upload whose response carried no id never reaches this point, the
	// client layer having already failed it (internal/play/appstore.trackingID).
	_, err := w.Write(p.Raw)
	return err
}

// Run is the business function the kernel invokes. It resolves the two
// addressing values and the local path, then, unless --dry-run short-circuits
// before any network and before the file is even opened: streams the document.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return nil, appstorecmd.Usagef("missing policy declaration file path: gplay appstore upload policy --%s <store-pkg> <file>", appstorecmd.FlagStorePackage)
	}
	storePackage, err := appstorecmd.ResolveStorePackage(in.StorePackage)
	if err != nil {
		return nil, err
	}
	pkg, err := appstorecmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}

	// Deliberately before os.Open: a rehearsal answers "what would this
	// command address?", and a job planning the upload of a document that is
	// still being assembled must still get an answer.
	if in.DryRun {
		return Payload{StorePackage: storePackage, Package: pkg, Path: path, DryRun: true}, nil
	}

	// UploadClient, not AuthedClient: a 10 MiB document on a slow CI uplink can
	// outlive the 60s control-plane default. It honors an explicit --timeout.
	httpClient, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}

	fileID, raw, err := appstore.UploadPolicyDeclarationFile(rc.Ctx, httpClient, storePackage, pkg, path)
	if err != nil {
		return nil, appstorecmd.ClassifyHostedApp(storePackage, pkg, err)
	}

	// DESIGN §8: a committed mutation prints one ✓ line on stderr; stdout stays
	// data-only. The line names the id AND its destination, because an id with
	// nowhere to go is a silent dead end.
	rc.Confirmf("uploaded policy declaration file %s for hosted app %s in app store %s: fileId %s; %s", path, pkg, storePackage, fileID, citeHint)
	return Payload{StorePackage: storePackage, Package: pkg, Path: path, FileID: fileID, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay appstore upload policy`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "policy <file>",
		Short: "Upload a policy declaration supporting document and print the fileId to cite in appstore update",
		Long: `Upload a supporting document for a hosted app's policy declarations, and print
the tracking id (fileId) Google assigns it.

The id is the point of the call. Nothing is declared here: the uploaded
document sits inert until a later ` + "`gplay appstore update`" + ` names its id inside a
policy response's documentResponse, which is how a declaration answer points at
the evidence backing it.

Two identifiers address the call, and mixing them up is the common mistake:

  --store-package  the app store's OWN package name (the caller: the
                   third-party store enrolled for alternative distribution),
                   falling back to $` + appstorecmd.EnvStorePackage + ` (ADR-0043)
  --package        the hosted app's package name (the subject), defaulting to
                   the repo's .gplay/config.json pin when omitted

The API accepts up to 10 MiB per document, as PDF, JPEG or PNG; the type is
sniffed from the file's leading bytes rather than its extension. The request
declares fileType=` + appstore.DeclarationFileTypeDocument + `, the enum's only
meaningful value, so there is no flag to set. The transfer is resumable.

The hosted app record must already exist: run ` + "`gplay appstore create`" + ` first.
The call is Edit-free: it opens no Edit and joins none.

No --confirm is required: an upload is inert (it produces an id and changes
nothing a user can see), so it fails the ADR-0043 gate criterion of being
irreversible AND externally visible. GPLAY_READONLY still refuses it (exit 4)
but lets --dry-run run.

--output json passes the API response through verbatim (ADR-0003). --dry-run
previews the resolved target with no HTTP call and without opening the file. An
unreadable path is a client-side failure (exit 20); a 403 names the app store
enrollment the call requires.`,
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
