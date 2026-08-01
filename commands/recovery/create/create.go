// Package create implements `gplay recovery create`: create a DRAFT app recovery
// action targeting users on a bad versionCode. A draft is harmless (not yet
// pushed to any user), so create is the ROUTINE tier (ADR-0017): --dry-run
// rehearses, no --confirm — the dangerous step is `recovery deploy`, which names
// itself. MarkMutating gates it under GPLAY_READONLY. Ships [experimental].
package create

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/recovery/recoverycmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package           string
	VersionCode       int64
	AllUsers          bool
	Regions           []string
	SdkLevels         []int64
	RemoteInAppUpdate bool
	DryRun            bool
}

// Payload renders the created draft (live) or the rehearsal (--dry-run).
type Payload struct {
	Package string
	DryRun  bool
	Row     recoverycmd.Row
	Cols    []output.Column[recoverycmd.Row]
	Raw     json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Cols, []recoverycmd.Row{p.Row}) },
	}
}

func (p Payload) renderTable(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "would create a draft recovery action for %s (dry-run)\n", p.Package)
		return err
	}
	return output.RenderTable(w, p.Cols, []recoverycmd.Row{p.Row})
}

type dryRunView struct {
	DryRun  bool   `json:"dryRun"`
	Package string `json:"package"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, Package: p.Package})
	}
	_, err := w.Write(p.Raw)
	return err
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.VersionCode <= 0 {
		return nil, recoverycmd.Usagef("missing or invalid --version-code: the bad APK versionCode the recovery targets is required")
	}
	if !in.AllUsers && len(in.Regions) == 0 && len(in.SdkLevels) == 0 {
		return nil, recoverycmd.Usagef("missing targeting: pass one of --all-users, --regions, or --sdk-levels")
	}
	cols, err := recoverycmd.ResolveColumns("")
	if err != nil {
		return nil, err
	}
	pkg, err := recoverycmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}

	if in.DryRun {
		return Payload{Package: pkg, DryRun: true, Cols: cols}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	a, raw, err := recovery.Create(rc.Ctx, httpClient, pkg, recovery.CreateOpts{
		VersionCodes:      []int64{in.VersionCode},
		AllUsers:          in.AllUsers,
		Regions:           in.Regions,
		SdkLevels:         in.SdkLevels,
		RemoteInAppUpdate: in.RemoteInAppUpdate,
	})
	if err != nil {
		return nil, recoverycmd.Classify(pkg, err)
	}

	rc.Confirmf("created draft recovery %s for %q (versionCode %d)", a.AppRecoveryID, pkg, in.VersionCode)
	return Payload{Package: pkg, Row: recoverycmd.BuildRow(a), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay recovery create`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a draft recovery action for a bad versionCode",
		Long: `Create a DRAFT app recovery action targeting users impacted by a bad APK
versionCode. A draft is staged but NOT yet pushed to any user — activate it
later with ` + "`gplay recovery deploy <id>`" + ` (which requires --confirm).

Pass --version-code (the bad version) and at least one audience selector:
--all-users, --regions <CC,CC>, or --sdk-levels <N,N>. The recovery uses a
remote in-app update by default (--remote-in-app-update).

A draft is harmless, so create needs no --confirm; use --dry-run to validate
inputs without any HTTP call. GPLAY_READONLY still refuses it (exit 4).`,
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
	cmd.Flags().Int64Var(&in.VersionCode, "version-code", 0, "the bad APK versionCode the recovery targets (required)")
	cmd.Flags().BoolVar(&in.AllUsers, "all-users", false, "target all users on the bad versionCode")
	cmd.Flags().StringSliceVar(&in.Regions, "regions", nil, "target users in these CLDR region codes (e.g. US,FR)")
	cmd.Flags().Int64SliceVar(&in.SdkLevels, "sdk-levels", nil, "target users on these Android SDK levels (e.g. 30,31)")
	cmd.Flags().BoolVar(&in.RemoteInAppUpdate, "remote-in-app-update", true, "use a remote in-app update (the only recovery type Play models today)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and resolve the target without any HTTP call")
	return cmd
}
