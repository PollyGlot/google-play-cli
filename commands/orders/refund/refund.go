// Package refund implements `gplay orders refund <orderId> --confirm [--revoke]`:
// the money-moving, irreversible refund of a single Google Play order
// (orders.refund, POST, no body). DESTRUCTIVE tier (ADR-0017): it refuses
// without --confirm, exiting 3 with a message naming the flag; CI=true never
// auto-confirms; MarkMutating so GPLAY_READONLY refuses it (exit 4); --dry-run
// previews the target with no HTTP and lists the gate in the `requires` array.
// --revoke (the API `revoke` query parameter) defaults false — refund the money
// but keep the entitlement; revoking access is the larger hammer, opt-in. There
// is deliberately no bulk/fan-out refund verb (ADR-0031). Refunding requires the
// service account to hold CAN_MANAGE_ORDERS (never part of a Role bundle); a 403
// names it, and orders older than 3 years surface a specific refusal. Ships
// [experimental] (ADR-0010).
package refund

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/orders/orderscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/orders"
)

// Input is the request-shaped struct cobra builds from flags + the orderId arg.
type Input struct {
	Package string
	OrderID string
	Revoke  bool
	Confirm bool
	DryRun  bool
}

// Payload renders what refund did (or, on --dry-run, would do). Requires is the
// safety gate surfaced under --dry-run --output json (ADR-0017 §4); Raw carries
// any verbatim body (orders.refund returns none on success, so it is usually
// empty and a gplay-shaped success object is emitted instead).
type Payload struct {
	OrderID  string
	Revoke   bool
	Requires []string
	DryRun   bool
	Raw      json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) verb() string {
	if p.DryRun {
		return "would refund"
	}
	return "refunded"
}

// revokeNote describes the entitlement outcome for the human views.
func (p Payload) revokeNote() string {
	if p.Revoke {
		return "revoke entitlement"
	}
	return "keep entitlement"
}

func (p Payload) renderTable(w io.Writer) error {
	suffix := ""
	if p.DryRun {
		suffix = " (dry-run)"
	}
	if _, err := fmt.Fprintf(w, "%s %s [%s]%s\n", p.verb(), p.OrderID, p.revokeNote(), suffix); err != nil {
		return err
	}
	if len(p.Requires) > 0 {
		if _, err := fmt.Fprintf(w, "requires: %s\n", strings.Join(p.Requires, ", ")); err != nil {
			return err
		}
	}
	return nil
}

func (p Payload) renderMarkdown(w io.Writer) error {
	suffix := ""
	if p.DryRun {
		suffix = " (dry-run)"
	}
	rows := [][]string{
		{"Order", p.OrderID},
		{"Action", p.verb()},
		{"Entitlement", p.revokeNote()},
	}
	if len(p.Requires) > 0 {
		rows = append(rows, []string{"Requires", strings.Join(p.Requires, ", ")})
	}
	if _, err := fmt.Fprintf(w, "## orders refund%s\n\n", suffix); err != nil {
		return err
	}
	return output.MarkdownTable(w, []string{"FIELD", "VALUE"}, rows)
}

type dryRunView struct {
	DryRun   bool     `json:"dryRun"`
	OrderID  string   `json:"orderId"`
	Revoke   bool     `json:"revoke"`
	Requires []string `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{DryRun: true, OrderID: p.OrderID, Revoke: p.Revoke, Requires: p.Requires})
	}
	// orders.refund returns an empty body on success; emit a gplay-shaped
	// success object so --output json (the CI default) is always parseable
	// rather than zero bytes (the documented ADR-0003 exception, as in
	// `team users remove`).
	if len(bytes.TrimSpace(p.Raw)) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, struct {
		OK       bool   `json:"ok"`
		Refunded string `json:"refunded"`
		Revoked  bool   `json:"revoked"`
	}{OK: true, Refunded: p.OrderID, Revoked: p.Revoke})
}

// Run is the business function the kernel invokes. It enforces the destructive
// --confirm gate (unless --dry-run previews offline) and refunds the order.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	orderID := strings.TrimSpace(in.OrderID)
	if orderID == "" {
		return nil, orderscmd.Usagef("no order — pass an order ID: gplay orders refund <orderId> --confirm")
	}

	// refund is unconditionally destructive (money-moving, irreversible), so the
	// gate is always a single required flag.
	requires := []string{"confirm"}

	if in.DryRun {
		return Payload{OrderID: orderID, Revoke: in.Revoke, Requires: requires, DryRun: true}, nil
	}

	if !in.Confirm {
		return nil, exit.SafetyFlag("confirm", "refunding order %q moves money and cannot be undone; pass --confirm to proceed (rehearse first with --dry-run)", orderID)
	}

	pkg, err := orderscmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	raw, err := orders.Refund(rc.Ctx, httpClient, pkg, orderID, in.Revoke)
	if err != nil {
		return nil, orderscmd.ClassifyRefund(pkg, orderID, err)
	}
	if in.Revoke {
		rc.Confirmf("refunded order %s for %q and revoked the entitlement", orderID, pkg)
	} else {
		rc.Confirmf("refunded order %s for %q (entitlement kept)", orderID, pkg)
	}
	return Payload{OrderID: orderID, Revoke: in.Revoke, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay orders refund <orderId>`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "refund <orderId>",
		Short: "[experimental] Refund a Google Play order (money-moving, irreversible)",
		Long: `Refund a single Google Play order by its order ID via orders.refund — a
money-moving, irreversible write. By default the money is returned but the
buyer keeps access to what they bought; pass --revoke to additionally terminate
the entitlement (for a subscription, all future payments stop too).

This is destructive, so it refuses without --confirm (exit 3, naming the flag);
CI=true never auto-confirms. Use --dry-run to preview the target with no HTTP
call — under --output json the preview lists the gate in a "requires" array.
GPLAY_READONLY refuses it (exit 4). There is no bulk refund — refund one order
at a time.

Refunding requires the service account to hold the CAN_MANAGE_ORDERS permission
(never part of a Role bundle); a 403 names it. Google does not allow refunding
orders older than 3 years — that surfaces as a specific refusal, not a generic
error.

[experimental] — the surface may still evolve (ADR-0010).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.OrderID = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().BoolVar(&in.Revoke, "revoke", false, "also revoke the buyer's entitlement (default: refund money, keep access)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the refund (required — this moves money and is irreversible)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "preview the target without any HTTP call (reports the --confirm requirement)")
	return cmd
}
