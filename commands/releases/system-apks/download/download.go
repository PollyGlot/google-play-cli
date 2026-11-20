package download

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/system-apks/systemapkscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/systemapks"
)

const destStdout = "-"

type Input struct {
	Package     string
	VersionCode int64
	VariantID   string
	Dest        string
}

type localIOError struct {
	path  string
	cause error
}

func (e *localIOError) Error() string { return fmt.Sprintf("write %s: %v", e.path, e.cause) }
func (e *localIOError) Unwrap() error { return e.cause }
func (e *localIOError) ExitCode() int { return 20 }

func Run(rc *kernel.RunContext, in Input) error {
	id, err := systemapkscmd.ParseVariantID(in.VariantID)
	if err != nil {
		return err
	}
	if err := systemapkscmd.RequireVersionCode(in.VersionCode); err != nil {
		return err
	}
	if strings.TrimSpace(in.Dest) == "" {
		return exit.Usagef("missing --dest: pass a file path, or --dest - to stream to stdout")
	}
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return err
	}
	httpClient, err := rc.UploadClient()
	if err != nil {
		return err
	}

	if in.Dest == destStdout {
		n, err := systemapks.Download(rc.Ctx, httpClient, pkg, in.VersionCode, id, rc.Stdout)
		if err != nil {
			return systemapkscmd.Classify(pkg, err)
		}
		rc.Confirmf("streamed %d bytes to stdout (variantId %d)", n, id)
		return nil
	}

	f, err := os.Create(in.Dest)
	if err != nil {
		return &localIOError{path: in.Dest, cause: err}
	}
	n, derr := systemapks.Download(rc.Ctx, httpClient, pkg, in.VersionCode, id, f)
	closeErr := f.Close()
	if derr != nil {
		_ = os.Remove(in.Dest)
		return systemapkscmd.Classify(pkg, derr)
	}
	if closeErr != nil {
		_ = os.Remove(in.Dest)
		return &localIOError{path: in.Dest, cause: closeErr}
	}
	rc.Confirmf("wrote %d bytes to %s (variantId %d)", n, in.Dest, id)
	return nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var in Input
	cmd := &cobra.Command{
		Use:   "download <variantId>",
		Short: "Download a system APK variant to a file (or stdout)",
		Long: `Fetch one system APK variant, streaming its raw bytes to disk. Address it by the
<variantId> from ` + "`gplay releases system-apks list`" + ` (or create) and the
--version-code of the bundle it was created from.

The destination is --dest PATH (required); --dest - streams the bytes to stdout
for piping. This command has no --output flag: its payload is raw bytes, not a
Renderable. On success a ✓ line on stderr names the byte count and destination.

This is a direct application-scoped read: it opens no Edit.`,
		Example: `  # Save variant 3 of versionCode 1042
  gplay releases system-apks download 3 --version-code 1042 --dest system.apk

  # Stream the bytes to another tool instead of a file
  gplay releases system-apks download 3 --version-code 1042 --dest - | sha256sum`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.VariantID = args[0]
			return kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
				return nil, Run(rc, in)
			})
		},
	}
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().Int64Var(&in.VersionCode, "version-code", 0, "the bundle versionCode the variant was created from (required)")
	cmd.Flags().StringVar(&in.Dest, "dest", "", "destination file path, or - to stream to stdout (required)")
	return cmd
}
