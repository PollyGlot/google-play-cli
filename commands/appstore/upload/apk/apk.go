// Package apk implements `gplay appstore upload apk --store-package <sp>
// [--package <pkg>] <file.apk>`: stream an APK for a hosted app to
// `appstoreappsreview.uploadApk` and print the tracking id Google assigns it.
//
// The id — not the transfer — is the product of the call. `gplay appstore
// update` cites it in `activeApks.activeApkSets[].baseApkId` and
// `.splitApkId[]`, so an operator uploads a binary ONCE and refers to it by id
// afterwards instead of re-sending gigabytes on every metadata change. Nothing
// is distributed by this command: an uploaded APK sits inert until an
// `appstore update` names its id.
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
package apk

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
const citeHint = "cite this apkId in `gplay appstore update` as activeApks.activeApkSets[].baseApkId (or .splitApkId[]) — the APK is distributed by that call, not by this one"

// Input is the request-shaped struct cobra builds from the flags plus the
// positional file path.
type Input struct {
	StorePackage string
	Package      string
	Path         string
	DryRun       bool
}

// Payload renders the uploaded APK's tracking id (or, on --dry-run, the upload
// that would happen). Raw carries the verbatim API body for the ADR-0003
// --output json pass-through; APKID is the same body's `apkId`, lifted out so
// the human views can lead with it.
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

// rows is the identifying set shared by the human views, in a fixed order with
// APK_ID first — it is the only value the caller has to carry forward.
func (p Payload) rows() [][2]string {
	return [][2]string{
		{"APK_ID", p.APKID},
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
	if _, err := fmt.Fprintf(w, "## appstore upload apk%s\n\n", suffix); err != nil {
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
// before any network and before the file is even opened — streams the APK.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return nil, appstorecmd.Usagef("missing APK path: gplay appstore upload apk --%s <store-pkg> <file.apk>", appstorecmd.FlagStorePackage)
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
	// command address?", and a CI job planning an upload of an artifact its
	// build step has not produced yet must still get an answer.
	if in.DryRun {
		return Payload{StorePackage: storePackage, Package: pkg, Path: path, DryRun: true}, nil
	}

	// UploadClient, not AuthedClient: the API accepts APKs up to 10 GiB, which
	// the 60s control-plane default would kill mid-transfer. It honors an
	// explicit --timeout.
	httpClient, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}

	apkID, raw, err := appstore.UploadAPK(rc.Ctx, httpClient, storePackage, pkg, path)
	if err != nil {
		return nil, appstorecmd.ClassifyReview(storePackage, err)
	}

	// DESIGN §8: a committed mutation prints one ✓ line on stderr; stdout stays
	// data-only. The line names the id AND its destination, because an id with
	// nowhere to go is a silent dead end.
	rc.Confirmf("uploaded APK %s for hosted app %s in app store %s — apkId %s; %s", path, pkg, storePackage, apkID, citeHint)
	return Payload{StorePackage: storePackage, Package: pkg, Path: path, APKID: apkID, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay appstore upload apk`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "apk <file.apk>",
		Short: "Upload an APK for a hosted app and print the apkId to cite in appstore update",
		Long: `Upload an APK for an app a third-party Android app store hosts, and print the
tracking id (apkId) Google assigns it.

The id is the point of the call. Nothing is distributed here: the uploaded APK
sits inert until a later ` + "`gplay appstore update`" + ` names its id in
activeApks.activeApkSets[].baseApkId (the base APK) or .splitApkId[] (the
splits). Upload the binary once, then cite the id — re-sending gigabytes on
every metadata change is never necessary.

Two identifiers address the call, and mixing them up is the common mistake:

  --store-package  the app store's OWN package name (the caller — the
                   third-party store enrolled for alternative distribution),
                   falling back to $` + appstorecmd.EnvStorePackage + ` (ADR-0043)
  --package        the hosted app's package name (the subject), defaulting to
                   the repo's .gplay/config.json pin when omitted

The API accepts up to 10 GiB per APK and the upload announces
application/octet-stream. The transfer is resumable, so a transient network
failure re-sends from the last byte the server acknowledged rather than
restarting. The command is exempt from the 60s control-plane timeout; pass the
global --timeout to bound it explicitly.

The hosted app record must already exist: run ` + "`gplay appstore create`" + ` first.
The call is Edit-free — it opens no Edit and joins none.

No --confirm is required: an upload is inert (it produces an id and changes
nothing a user can see), so it fails the ADR-0043 gate criterion of being
irreversible AND externally visible. GPLAY_READONLY still refuses it (exit 4)
but lets --dry-run run.

--output json passes the API response through verbatim (ADR-0003). --dry-run
previews the resolved target with no HTTP call and without opening the file, so
it works before the artifact is built. An unreadable path is a client-side
failure (exit 20); a 403 names the app store enrollment the call requires.`,
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
