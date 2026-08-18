// Package image implements `gplay appstore upload image --store-package <sp>
// [--package <pkg>] <file>`: stream a store image for a hosted app to
// `appstoreappsreview.uploadImage` and print the tracking id Google assigns it.
//
// The id — not the transfer — is the product of the call. `gplay appstore
// update` cites it as the listing's `appIconId` or as one of its
// `screenshotId[]` entries, so an operator uploads an asset ONCE and refers to
// it by id afterwards. Nothing is shown to a user by this command: an uploaded
// image is inert until an `appstore update` names its id.
//
// Two addressing values meet here, as everywhere in the namespace:
// --store-package names the calling app store (the `appstore/{...}` path key),
// --package names the hosted app (a path segment of its own), defaulting to
// the project pin.
//
// MarkMutating at registration so GPLAY_READONLY refuses it (exit 4); --dry-run
// previews the resolved target without any HTTP call AND without opening the
// file. Edit-free — the call is not under `/edits/`. No confirmation gate
// (ADR-0043): a gate is for the irreversible *and* externally visible, and an
// upload is neither — it mints an id and changes nothing a user can see.
package image

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
const citeHint = "cite this imageId in `gplay appstore update` as the listing's appIconId or one of its screenshotId[] entries — the image is shown by that call, not by this one"

// Input is the request-shaped struct cobra builds from the flags plus the
// positional file path.
type Input struct {
	StorePackage string
	Package      string
	Path         string
	DryRun       bool
}

// Payload renders the uploaded image's tracking id (or, on --dry-run, the
// upload that would happen). Raw carries the verbatim API body for the ADR-0003
// --output json pass-through; ImageID is the same body's `imageId`, lifted out
// so the human views can lead with it.
type Payload struct {
	StorePackage string
	Package      string
	Path         string
	ImageID      string
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
// IMAGE_ID first — it is the only value the caller has to carry forward.
func (p Payload) rows() [][2]string {
	return [][2]string{
		{"IMAGE_ID", p.ImageID},
		{"APP_STORE_PACKAGE_NAME", p.StorePackage},
		{"PACKAGE_NAME", p.Package},
		{"FILE", p.Path},
	}
}

// renderTable writes `FIELD<TAB>VALUE` lines (like `apps view` / `orders
// view`), then the cite hint — the id alone does not tell an operator where it
// is meant to go. A dry-run leads with the rehearsed action instead.
func (p Payload) renderTable(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "would upload image %s for hosted app %s in app store %s (dry-run)\n", p.Path, p.Package, p.StorePackage)
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
	if _, err := fmt.Fprintf(w, "## appstore upload image%s\n\n", suffix); err != nil {
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
// machine-readable `requires` array (ADR-0017 §4) — empty, because an upload
// needs no safety flag beyond a writable environment.
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
	// ADR-0003: the API response is passed through verbatim — it is where the
	// tracking id comes from in the first place. There is no empty-body
	// fallback here, unlike the acknowledgement-only verbs in this namespace:
	// an upload whose response carried no id never reaches this point, the
	// client layer having already failed it (internal/play/appstore.trackingID).
	_, err := w.Write(p.Raw)
	return err
}

// Run is the business function the kernel invokes. It resolves the two
// addressing values and the local path, then — unless --dry-run short-circuits
// before any network and before the file is even opened — streams the image.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return nil, appstorecmd.Usagef("missing image path: gplay appstore upload image --%s <store-pkg> <file>", appstorecmd.FlagStorePackage)
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
	// command address?", and a CI job planning an upload of an asset its
	// build step has not generated yet must still get an answer.
	if in.DryRun {
		return Payload{StorePackage: storePackage, Package: pkg, Path: path, DryRun: true}, nil
	}

	// UploadClient, not AuthedClient: a 15 MiB asset on a slow CI uplink can
	// outlive the 60s control-plane default. It honors an explicit --timeout.
	httpClient, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}

	imageID, raw, err := appstore.UploadImage(rc.Ctx, httpClient, storePackage, pkg, path)
	if err != nil {
		return nil, appstorecmd.ClassifyReview(storePackage, err)
	}

	// DESIGN §8: a committed mutation prints one ✓ line on stderr; stdout stays
	// data-only. The line names the id AND its destination, because an id with
	// nowhere to go is a silent dead end.
	rc.Confirmf("uploaded image %s for hosted app %s in app store %s — imageId %s; %s", path, pkg, storePackage, imageID, citeHint)
	return Payload{StorePackage: storePackage, Package: pkg, Path: path, ImageID: imageID, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay appstore upload image`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "image <file>",
		Short: "Upload a store image for a hosted app and print the imageId to cite in appstore update",
		Long: `Upload a store image (app icon or screenshot) for an app a third-party Android
app store hosts, and print the tracking id (imageId) Google assigns it.

The id is the point of the call. Nothing is shown to a user here: the uploaded
image sits inert until a later ` + "`gplay appstore update`" + ` names its id as the
listing's appIconId or as one of its screenshotId[] entries. Upload the asset
once, then cite the id.

Two identifiers address the call, and mixing them up is the common mistake:

  --store-package  the app store's OWN package name (the caller — the
                   third-party store enrolled for alternative distribution),
                   falling back to $` + appstorecmd.EnvStorePackage + ` (ADR-0043)
  --package        the hosted app's package name (the subject), defaulting to
                   the repo's .gplay/config.json pin when omitted

The API accepts up to 15 MiB per image and only image/* content. The type is
sniffed from the file's leading bytes rather than its extension, so a PNG named
` + "`icon`" + ` with no suffix uploads correctly — and a file that is not an image is
rejected by the API, not silently accepted. The transfer is resumable.

The hosted app record must already exist: run ` + "`gplay appstore create`" + ` first.
The call is Edit-free — it opens no Edit and joins none.

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
