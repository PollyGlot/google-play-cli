// Package download implements `gplay releases generated download <downloadId>`:
// fetch one APK Play generated from a bundle, streaming the raw signed bytes to
// a local file (or stdout with `--dest -`). This is gplay's first binary
// download-to-file gesture (ADR-0034): `download` is a domain verb (it writes
// opaque bytes, which `view`/`set` cannot state honestly), the payload is raw
// bytes (no --output), and the destination is --dest. Read-only and Edit-free;
// not MarkMutating, not gated by GPLAY_READONLY. Ships [experimental].
package download

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/generated/generatedcmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/generatedapks"
)

// destStdout is the --dest sentinel that streams the bytes to stdout for piping.
const destStdout = "-"

// Input is the request-shaped struct cobra builds from the positional + flags.
type Input struct {
	Package     string
	VersionCode int64
	DownloadID  string
	Dest        string
}

// localIOError is returned when the destination file cannot be created or
// finalized. It is distinct from *api.Error so the exit code maps to client-side
// IO (20 per docs/DESIGN.md §9) rather than transport (50): mirroring
// internal/play/sharing.LocalIOError.
type localIOError struct {
	path  string
	cause error
}

func (e *localIOError) Error() string { return fmt.Sprintf("write %s: %v", e.path, e.cause) }
func (e *localIOError) Unwrap() error { return e.cause }
func (e *localIOError) ExitCode() int { return 20 }

// Run streams the addressed artifact to the destination and emits the success ✓
// on stderr. It writes the data path itself (binary file or stdout) and returns
// no Renderable: the bytes are not a Renderable (DESIGN §"Commands without
// --output"), so the kernel renders nothing.
func Run(rc *kernel.RunContext, in Input) error {
	if strings.TrimSpace(in.DownloadID) == "" {
		return generatedcmd.Usagef("missing <downloadId>: the artifact to download, from `gplay releases generated list`")
	}
	if in.VersionCode <= 0 {
		return generatedcmd.Usagef("missing or invalid --version-code: the bundle versionCode the artifact was generated from is required")
	}
	if strings.TrimSpace(in.Dest) == "" {
		return generatedcmd.Usagef("missing --dest: pass a file path, or --dest - to stream to stdout")
	}
	pkg, err := generatedcmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return err
	}

	if in.Dest == destStdout {
		n, err := generatedapks.Download(rc.Ctx, httpClient, pkg, in.VersionCode, in.DownloadID, rc.Stdout)
		if err != nil {
			return generatedcmd.Classify(pkg, err)
		}
		rc.Confirmf("streamed %d bytes to stdout (downloadId %s)", n, in.DownloadID)
		return nil
	}

	f, err := os.Create(in.Dest)
	if err != nil {
		return &localIOError{path: in.Dest, cause: err}
	}
	n, derr := generatedapks.Download(rc.Ctx, httpClient, pkg, in.VersionCode, in.DownloadID, f)
	closeErr := f.Close()
	// Don't leave a partial/corrupt APK behind: a failed transfer OR a Close
	// failure (buffered bytes that never reached disk) means the file is suspect.
	if derr != nil {
		_ = os.Remove(in.Dest)
		return generatedcmd.Classify(pkg, derr)
	}
	if closeErr != nil {
		_ = os.Remove(in.Dest)
		return &localIOError{path: in.Dest, cause: closeErr}
	}
	rc.Confirmf("wrote %d bytes to %s (downloadId %s)", n, in.Dest, in.DownloadID)
	return nil
}

// NewCommand returns the cobra command for `gplay releases generated download`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var in Input
	cmd := &cobra.Command{
		Use:   "download <downloadId>",
		Short: "Download a generated APK to a file (or stdout)",
		Long: `Fetch one APK Play generated and signed from an uploaded bundle, streaming the
raw signed bytes to disk. Address the artifact by the <downloadId> from
` + "`gplay releases generated list`" + ` and the --version-code it was generated from.

The destination is --dest PATH (required); --dest - streams the bytes to stdout
for piping. This command has no --output flag: its payload is raw bytes, not a
Renderable. On success a ✓ line on stderr names the byte count and destination.

This is a direct application-scoped read: it opens no Edit and moves no money.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.DownloadID = args[0]
			return kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
				return nil, Run(rc, in)
			})
		},
	}
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().Int64Var(&in.VersionCode, "version-code", 0, "the bundle versionCode the artifact was generated from (required)")
	cmd.Flags().StringVar(&in.Dest, "dest", "", "destination file path, or - to stream to stdout (required)")
	return cmd
}
