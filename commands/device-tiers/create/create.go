// Package create implements `gplay device-tiers create`: create a new (immutable)
// Device Tier Config on Play from a JSON DeviceTierConfig body read from --file
// or stdin. The resource is app-scoped and OUTSIDE the Edit lifecycle, and the
// API has no update/patch/delete, so create is the ROUTINE tier (ADR-0017):
// --dry-run rehearses, no --confirm (a create can never overwrite or destroy).
// MarkMutating gates it under GPLAY_READONLY (exit 4). Ships [experimental].
package create

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/device-tiers/devicetierscmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/devicetiers"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package             string
	File                string // "" or "-" → stdin
	AllowUnknownDevices bool
	DryRun              bool
}

// validationError is a client-side validation failure (exit 20): an unreadable
// or empty body, malformed JSON, or a body carrying the output-only
// deviceTierConfigId (a sign the caller wants an update, which the API has no
// method for).
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func (e *validationError) ExitCode() int { return 20 }

// Payload renders the created config (live) or the rehearsal (--dry-run).
type Payload struct {
	Package string
	Bytes   int
	DryRun  bool
	Row     devicetierscmd.Row
	Cols    []output.Column[devicetierscmd.Row]
	Raw     json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Cols, []devicetierscmd.Row{p.Row}) },
	}
}

func (p Payload) renderTable(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "would create a device tier config (%d bytes) for %s (dry-run)\n", p.Bytes, p.Package)
		return err
	}
	return output.RenderTable(w, p.Cols, []devicetierscmd.Row{p.Row})
}

type dryRunView struct {
	DryRun  bool   `json:"dryRun"`
	Package string `json:"package"`
	Bytes   int    `json:"bytes"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, Package: p.Package, Bytes: p.Bytes})
	}
	_, err := w.Write(p.Raw)
	return err
}

// readBody reads the config body from --file (or stdin when "" / "-").
func readBody(rc *kernel.RunContext, file string) ([]byte, error) {
	file = strings.TrimSpace(file)
	if file == "" || file == "-" {
		if rc.Stdin == nil {
			return nil, &validationError{msg: "no --file and no stdin to read the DeviceTierConfig from"}
		}
		b, err := io.ReadAll(rc.Stdin)
		if err != nil {
			return nil, &validationError{msg: "cannot read DeviceTierConfig from stdin: " + err.Error()}
		}
		return b, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, &validationError{msg: fmt.Sprintf("cannot read --file %s: %v", file, err)}
	}
	return b, nil
}

// validateBody enforces: non-empty, valid JSON, and NO deviceTierConfigId (it is
// output-only; its presence means the caller wants an update the API lacks).
func validateBody(body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return &validationError{msg: "the DeviceTierConfig body is empty: pass --file <path> or pipe JSON on stdin"}
	}
	if !json.Valid(body) {
		return &validationError{msg: "the DeviceTierConfig body is not valid JSON"}
	}
	var probe struct {
		ID json.RawMessage `json:"deviceTierConfigId"`
	}
	if err := json.Unmarshal(body, &probe); err == nil && len(probe.ID) > 0 && string(probe.ID) != "null" {
		return &validationError{msg: "remove deviceTierConfigId from the body: it is server-assigned and device tier configs are immutable (the API has no update method)"}
	}
	return nil
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	cols, err := devicetierscmd.ResolveColumns("")
	if err != nil {
		return nil, err
	}
	body, err := readBody(rc, in.File)
	if err != nil {
		return nil, err
	}
	if err := validateBody(body); err != nil {
		return nil, err
	}
	pkg, err := devicetierscmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}

	if in.DryRun {
		return Payload{Package: pkg, Bytes: len(body), DryRun: true, Cols: cols}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	cfg, raw, err := devicetiers.Create(rc.Ctx, httpClient, pkg, body, in.AllowUnknownDevices)
	if err != nil {
		return nil, devicetierscmd.Classify(pkg, err)
	}

	rc.Confirmf("created device tier config %s for %q", cfg.DeviceTierConfigID, pkg)
	return Payload{Package: pkg, Bytes: len(body), Row: devicetierscmd.BuildRow(cfg), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay device-tiers create`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a device tier config from a JSON body",
		Long: `Create a new device tier config on Google Play from a JSON DeviceTierConfig
body read from --file (or stdin when --file is omitted or "-"). The server
assigns the deviceTierConfigId; do not include it in the body.

Device tier configs are immutable: the API has create/get/list only, no
update or delete, so create needs no --confirm (it can never overwrite or
destroy an existing config); use --dry-run to validate the body and resolve the
target without any HTTP call. GPLAY_READONLY still refuses it (exit 4).`,
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
	cmd.Flags().StringVar(&in.File, "file", "", "path to a JSON DeviceTierConfig body (default: stdin, or '-')")
	cmd.Flags().BoolVar(&in.AllowUnknownDevices, "allow-unknown-devices", false, "allow devices unknown to Play in the config (sends allowUnknownDevices=true)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate the body and resolve the target without any HTTP call")
	return cmd
}
