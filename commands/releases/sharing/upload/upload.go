// Package upload implements `gplay releases sharing upload`: upload an APK or
// AAB to Google Play Internal App Sharing and print the private, shareable
// download link (CONTEXT.md: Internal App Sharing). It bypasses tracks and the
// Edit lifecycle: a QA/preview gesture, not a release. APK vs AAB is
// auto-detected by file extension (.apk / .aab) with a --format override.
//
// Write-safety is the ROUTINE tier (ADR-0017): no --confirm (it mints only a
// private link, touches no track or production release). It is still a Play
// state mutation, so the command is MarkMutating: GPLAY_READONLY refuses it
// with exit 4 (ADR-0024), and --dry-run validates the package + the local
// artifact with zero network I/O. Ships [experimental] (ADR-0010).
package upload

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/sharing"
)

// Input is the request-shaped struct cobra builds from flags + args.
type Input struct {
	Package      string
	ArtifactPath string
	Format       string // "" | "apk" | "bundle": overrides extension auto-detect
	DryRun       bool
}

// usageError is a CLI-misuse error with ExitCode()=2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// classifyError signals that gplay could not determine or read the artifact: an
// unrecognized extension with no --format, or a missing/unreadable/non-regular
// path. It is a client-side validation failure (exit 20), matching the way
// internal/play/sharing reports a bad path on the live path, so the same exit
// code applies whether the file is rejected here (dry-run + live pre-check) or
// inside the uploader.
type classifyError struct {
	path  string
	msg   string
	cause error
}

func (e *classifyError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.path, e.msg, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.path, e.msg)
}
func (e *classifyError) Unwrap() error { return e.cause }
func (e *classifyError) ExitCode() int { return 20 }

// artifact is the classified local file: its kind ("apk"|"bundle") and size.
type artifact struct {
	kind string
	size int64
}

// classify resolves the artifact kind from --format or the file extension, and
// stats the file (rejecting non-regular paths up front). All failures are
// exit-20 client-side validation, so a bad path or ambiguous format fails the
// same way on --dry-run (offline) and on the live path.
func classify(path, formatOverride string) (artifact, error) {
	kind := strings.ToLower(strings.TrimSpace(formatOverride))
	switch kind {
	case "apk", "bundle":
		// explicit override wins
	case "":
		switch strings.ToLower(filepath.Ext(path)) {
		case ".apk":
			kind = "apk"
		case ".aab":
			kind = "bundle"
		default:
			return artifact{}, &classifyError{path: path, msg: "cannot tell APK from AAB by extension: pass --format apk|bundle"}
		}
	default:
		return artifact{}, &usageError{msg: "--format must be apk or bundle"}
	}

	info, err := os.Stat(path)
	if err != nil {
		return artifact{}, &classifyError{path: path, msg: "cannot read artifact", cause: err}
	}
	if !info.Mode().IsRegular() {
		return artifact{}, &classifyError{path: path, msg: "not a regular file"}
	}
	return artifact{kind: kind, size: info.Size()}, nil
}

// Payload renders what the command did (live) or would do (--dry-run).
type Payload struct {
	Package  string
	Kind     string
	Bytes    int64
	DryRun   bool
	Artifact sharing.Artifact
	Raw      json.RawMessage
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
		_, err := fmt.Fprintf(w, "would upload %s (%d bytes) to Internal App Sharing for %s (dry-run)\n", p.Kind, p.Bytes, p.Package)
		return err
	}
	// downloadUrl leads: it is the whole point of the command.
	if _, err := fmt.Fprintf(w, "downloadUrl:             %s\n", p.Artifact.DownloadURL); err != nil {
		return err
	}
	if p.Artifact.SHA256 != "" {
		if _, err := fmt.Fprintf(w, "sha256:                  %s\n", p.Artifact.SHA256); err != nil {
			return err
		}
	}
	if p.Artifact.CertificateFingerprint != "" {
		if _, err := fmt.Fprintf(w, "certificateFingerprint:  %s\n", p.Artifact.CertificateFingerprint); err != nil {
			return err
		}
	}
	return nil
}

// dryRunView is the gplay-shaped dry-run report (unmistakable from a live
// pass-through: it carries dryRun:true). Routine tier → no `requires` array
// (that field is only for exit-3 gated writes, DESIGN §7 / ADR-0017).
type dryRunView struct {
	DryRun  bool   `json:"dryRun"`
	Package string `json:"package"`
	Format  string `json:"format"`
	Bytes   int64  `json:"bytes"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, Package: p.Package, Format: p.Kind, Bytes: p.Bytes})
	}
	// Live: verbatim API pass-through of the InternalAppSharingArtifact (ADR-0003).
	_, err := w.Write(p.Raw)
	return err
}

func (p Payload) renderMarkdown(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "- **dry-run**: would upload %s (%d bytes) to Internal App Sharing for `%s`\n", p.Kind, p.Bytes, p.Package)
		return err
	}
	if _, err := fmt.Fprintf(w, "- **downloadUrl**: %s\n", p.Artifact.DownloadURL); err != nil {
		return err
	}
	if p.Artifact.SHA256 != "" {
		if _, err := fmt.Fprintf(w, "- **sha256**: %s\n", p.Artifact.SHA256); err != nil {
			return err
		}
	}
	if p.Artifact.CertificateFingerprint != "" {
		if _, err := fmt.Fprintf(w, "- **certificateFingerprint**: %s\n", p.Artifact.CertificateFingerprint); err != nil {
			return err
		}
	}
	return nil
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if strings.TrimSpace(in.ArtifactPath) == "" {
		return nil, &usageError{msg: "missing artifact path: gplay releases sharing upload <app.apk|app.aab>"}
	}
	pkg := strings.TrimSpace(in.Package)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
	}

	art, err := classify(in.ArtifactPath, in.Format)
	if err != nil {
		return nil, err
	}

	if in.DryRun {
		// Full rehearsal minus the upload: package + artifact validated, zero HTTP.
		return Payload{Package: pkg, Kind: art.kind, Bytes: art.size, DryRun: true}, nil
	}

	// UploadClient, not AuthedClient: the artifact can be multiple GB, so it is
	// exempt from the 60s control-plane default (honors an explicit --timeout).
	httpClient, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}

	var (
		artResp sharing.Artifact
		raw     json.RawMessage
	)
	if art.kind == "apk" {
		artResp, raw, err = sharing.UploadAPK(rc.Ctx, httpClient, pkg, in.ArtifactPath)
	} else {
		artResp, raw, err = sharing.UploadBundle(rc.Ctx, httpClient, pkg, in.ArtifactPath)
	}
	if err != nil {
		return nil, err
	}

	// DESIGN §8: a committed mutation prints one ✓ line on stderr (never on --dry-run).
	rc.Confirmf("uploaded %s to Internal App Sharing for %q: %s", art.kind, pkg, artResp.DownloadURL)
	return Payload{Package: pkg, Kind: art.kind, Bytes: art.size, Artifact: artResp, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay releases sharing upload`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "upload <app.apk|app.aab>",
		Short: "Upload a build to Internal App Sharing and print its shareable link",
		Long: `Upload an APK or AAB to Google Play Internal App Sharing and print the
private, shareable downloadUrl an authorized tester follows to install it.
This bypasses tracks and the Edit lifecycle entirely: a QA/preview gesture,
not a release.

APK vs AAB is auto-detected by file extension (.apk / .aab); pass
--format apk|bundle to override when the extension is ambiguous.

--output json passes the InternalAppSharingArtifact through verbatim
(downloadUrl, certificateFingerprint, sha256). --dry-run validates the package
and the local artifact without any HTTP call. No --confirm is required (the
link is private and creates no track or production release); GPLAY_READONLY
still refuses it (exit 4).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.ArtifactPath = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Format, "format", "", "artifact type: apk or bundle (overrides extension auto-detect)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate the package and artifact without any HTTP call")
	return cmd
}
