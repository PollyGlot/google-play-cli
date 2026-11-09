// Package create implements `gplay releases system-apks create`: ask Play to
// build a System APK variant (an APK for a device system image) from the App
// Bundle uploaded under a versionCode, for one device spec and set of options.
// The Variant body is small and flat, so it is built from flags rather than a
// --file document. Application-scoped and Edit-free. A create adds a variant
// and can never overwrite or destroy one, so it is the ROUTINE tier
// (ADR-0017): --dry-run rehearses, no --confirm. MarkMutating gates it under
// GPLAY_READONLY (exit 4). Ships [experimental].
package create

import (
	"fmt"
	"io"
	"math"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/system-apks/systemapkscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/systemapks"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package                string
	VersionCode            int64
	ABIs                   []string
	ScreenDensity          int
	Locales                []string
	RotatedKey             bool
	UncompressedDex        bool
	UncompressedNativeLibs bool
	DryRun                 bool
}

// variant is the request body the flags describe. Nil sub-objects stay off
// the wire, so an unset option is the API default rather than an explicit
// false.
func (in Input) variant() systemapks.Variant {
	var v systemapks.Variant
	if len(in.ABIs) > 0 || in.ScreenDensity != 0 || len(in.Locales) > 0 {
		v.DeviceSpec = &systemapks.DeviceSpec{
			ScreenDensity:    uint32(in.ScreenDensity),
			SupportedAbis:    in.ABIs,
			SupportedLocales: in.Locales,
		}
	}
	if in.RotatedKey || in.UncompressedDex || in.UncompressedNativeLibs {
		v.Options = &systemapks.Options{
			Rotated:                     in.RotatedKey,
			UncompressedDexFiles:        in.UncompressedDex,
			UncompressedNativeLibraries: in.UncompressedNativeLibs,
		}
	}
	return v
}

// Payload renders the created variant (live) or the rehearsal (--dry-run).
type Payload struct {
	Package     string
	VersionCode int64
	DryRun      bool
	Request     systemapks.Variant
	Row         systemapkscmd.Row
	Cols        []output.Column[systemapkscmd.Row]
	Raw         []byte
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Cols, []systemapkscmd.Row{p.Row}) },
	}
}

func (p Payload) renderTable(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "would create a system APK variant of %s versionCode %d (dry-run)\n", p.Package, p.VersionCode)
		if err != nil {
			return err
		}
	}
	return output.RenderTable(w, p.Cols, []systemapkscmd.Row{p.Row})
}

// dryRunView carries the request body that would be sent and the ADR-0017
// `requires` array: empty, because create is the ROUTINE tier.
type dryRunView struct {
	DryRun      bool               `json:"dryRun"`
	Package     string             `json:"package"`
	VersionCode int64              `json:"versionCode"`
	Variant     systemapks.Variant `json:"variant"`
	Requires    []string           `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, Package: p.Package, VersionCode: p.VersionCode, Variant: p.Request, Requires: []string{}})
	}
	_, err := w.Write(p.Raw)
	return err
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if err := systemapkscmd.RequireVersionCode(in.VersionCode); err != nil {
		return nil, err
	}
	// The API types screenDensity uint32; refuse what it cannot hold locally.
	if in.ScreenDensity < 0 || in.ScreenDensity > math.MaxUint32 {
		return nil, exit.Usagef("invalid --screen-density %d: want a dpi value such as 480", in.ScreenDensity)
	}
	cols, err := systemapkscmd.ResolveColumns("")
	if err != nil {
		return nil, err
	}
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	req := in.variant()

	if in.DryRun {
		row := systemapkscmd.BuildRow(req)
		row.VariantID = "" // server-assigned: none exists before the create
		return Payload{Package: pkg, VersionCode: in.VersionCode, DryRun: true, Request: req, Row: row, Cols: cols}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	v, raw, err := systemapks.Create(rc.Ctx, httpClient, pkg, in.VersionCode, req)
	if err != nil {
		return nil, systemapkscmd.Classify(pkg, err)
	}
	rc.Confirmf("created system APK variant %d of %q versionCode %d", v.VariantID, pkg, in.VersionCode)
	return Payload{Package: pkg, VersionCode: in.VersionCode, Request: req, Row: systemapkscmd.BuildRow(v), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay releases system-apks create`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a system APK variant from an uploaded AAB",
		Long: `Ask Play to build an APK suitable for inclusion in a device system image
(preload / OEM) from the App Bundle uploaded under --version-code. The device
spec (--abi, --screen-density, --locale) and the build options
(--rotated-key, --uncompressed-dex, --uncompressed-native-libs) shape the
variant; the server assigns its variantId, which
` + "`gplay releases system-apks download`" + ` then fetches.

A create only adds a variant and never overwrites one, so it needs no
--confirm; --dry-run prints the request body and resolves the target without
any HTTP call. GPLAY_READONLY still refuses it (exit 4). --output json passes
the created Variant through verbatim.

This is a direct application-scoped write: it opens no Edit.`,
		Example: `  # Rehearse: print the request body, no HTTP call
  gplay releases system-apks create --version-code 1042 --abi arm64-v8a --screen-density 480 --locale en-US --dry-run

  # Create it with uncompressed native libraries
  gplay releases system-apks create --version-code 1042 --abi arm64-v8a --abi armeabi-v7a --screen-density 480 --uncompressed-native-libs --output json`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().Int64Var(&in.VersionCode, "version-code", 0, "the versionCode of the uploaded App Bundle to build from (required)")
	cmd.Flags().StringArrayVar(&in.ABIs, "abi", nil, "supported ABI, as the platform reports it, in order of preference (repeatable, e.g. arm64-v8a)")
	cmd.Flags().IntVar(&in.ScreenDensity, "screen-density", 0, "screen density in dpi (e.g. 480)")
	cmd.Flags().StringArrayVar(&in.Locales, "locale", nil, "installed locale as a BCP-47 tag (repeatable, e.g. en-US)")
	cmd.Flags().BoolVar(&in.RotatedKey, "rotated-key", false, "sign the variant with the rotated app signing key")
	cmd.Flags().BoolVar(&in.UncompressedDex, "uncompressed-dex", false, "store dex files uncompressed in the variant")
	cmd.Flags().BoolVar(&in.UncompressedNativeLibs, "uncompressed-native-libs", false, "store native libraries uncompressed in the variant")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "print the request body and resolve the target without any HTTP call")
	return cmd
}
