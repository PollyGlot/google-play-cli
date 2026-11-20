package create

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/recovery/recoverycmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
)

type Input struct {
	Package           string
	VersionCode       int64
	VersionRange      string
	AllUsers          bool
	Regions           []string
	SdkLevels         []int64
	RemoteInAppUpdate bool
	DryRun            bool
}

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
	DryRun   bool     `json:"dryRun"`
	Package  string   `json:"package"`
	Requires []string `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, Package: p.Package, Requires: []string{}})
	}
	_, err := w.Write(p.Raw)
	return err
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	opts, err := versionTargeting(in)
	if err != nil {
		return nil, err
	}
	if !in.AllUsers && len(in.Regions) == 0 && len(in.SdkLevels) == 0 {
		return nil, exit.Usagef("missing targeting: pass one of --all-users, --regions, or --sdk-levels")
	}
	cols, err := recoverycmd.ResolveColumns("")
	if err != nil {
		return nil, err
	}
	pkg, err := rc.Package(in.Package)
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
	opts.AllUsers, opts.Regions, opts.SdkLevels, opts.RemoteInAppUpdate = in.AllUsers, in.Regions, in.SdkLevels, in.RemoteInAppUpdate
	a, raw, err := recovery.Create(rc.Ctx, httpClient, pkg, opts)
	if err != nil {
		return nil, recoverycmd.Classify(pkg, err)
	}

	rc.Confirmf("created draft recovery %s for %q (%s)", a.AppRecoveryID, pkg, versionLabel(opts))
	return Payload{Package: pkg, Row: recoverycmd.BuildRow(a), Cols: cols, Raw: raw}, nil
}

func versionTargeting(in Input) (recovery.CreateOpts, error) {
	switch {
	case in.VersionRange != "" && in.VersionCode != 0:
		return recovery.CreateOpts{}, exit.Usagef("--version-code and --version-range both name the targeted versions: pass one")
	case in.VersionRange != "":
		r, err := parseVersionRange(in.VersionRange)
		if err != nil {
			return recovery.CreateOpts{}, err
		}
		return recovery.CreateOpts{VersionRange: r}, nil
	case in.VersionCode > 0:
		return recovery.CreateOpts{VersionCodes: []int64{in.VersionCode}}, nil
	default:
		return recovery.CreateOpts{}, exit.Usagef("missing or invalid --version-code: the bad APK versionCode the recovery targets is required (or --version-range for several)")
	}
}

func parseVersionRange(s string) (*recovery.VersionRange, error) {
	lo, hi, ok := strings.Cut(s, "-")
	start, errStart := strconv.ParseInt(strings.TrimSpace(lo), 10, 64)
	end, errEnd := strconv.ParseInt(strings.TrimSpace(hi), 10, 64)
	if !ok || errStart != nil || errEnd != nil || start <= 0 || end < start {
		return nil, exit.Usagef("invalid --version-range %q: want START-END, two positive versionCodes with START <= END (e.g. 1040-1042)", s)
	}
	return &recovery.VersionRange{Start: start, End: end}, nil
}

func versionLabel(opts recovery.CreateOpts) string {
	if r := opts.VersionRange; r != nil {
		return fmt.Sprintf("versionCodes %d-%d", r.Start, r.End)
	}
	return fmt.Sprintf("versionCode %d", opts.VersionCodes[0])
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a draft recovery action for a bad versionCode",
		Long: `Create a DRAFT app recovery action targeting users impacted by a bad APK
versionCode. A draft is staged but NOT yet pushed to any user: activate it
later with ` + "`gplay recovery deploy <id>`" + ` (which requires --confirm).

Pass --version-code (the bad version), or --version-range START-END for
every versionCode in between (both inclusive), and at least one audience
selector: --all-users, --regions <CC,CC>, or --sdk-levels <N,N>. The recovery uses a
remote in-app update by default (--remote-in-app-update).

A draft is harmless, so create needs no --confirm; use --dry-run to validate
inputs without any HTTP call. GPLAY_READONLY still refuses it (exit 4).`,
		Example: `  # Validate a draft Recovery for every user of the bad versionCode
  gplay recovery create --version-code 1042 --all-users --dry-run

  # Stage a draft for users in two regions only
  gplay recovery create --version-code 1042 --regions US,FR

  # Cover three bad versions at once
  gplay recovery create --version-range 1040-1042 --all-users`,
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
	cmd.Flags().Int64Var(&in.VersionCode, "version-code", 0, "the bad APK versionCode the recovery targets (this or --version-range is required)")
	cmd.Flags().StringVar(&in.VersionRange, "version-range", "", "the bad versionCodes as an inclusive range START-END, e.g. 1040-1042")
	cmd.Flags().BoolVar(&in.AllUsers, "all-users", false, "target all users on the bad versionCode")
	cmd.Flags().StringSliceVar(&in.Regions, "regions", nil, "target users in these CLDR region codes (e.g. US,FR)")
	cmd.Flags().Int64SliceVar(&in.SdkLevels, "sdk-levels", nil, "target users on these Android SDK levels (e.g. 30,31)")
	cmd.Flags().BoolVar(&in.RemoteInAppUpdate, "remote-in-app-update", true, "use a remote in-app update (the only recovery type Play models today)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and resolve the target without any HTTP call")
	return cmd
}
