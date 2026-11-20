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

type Input struct {
	Package string
	OrderID string
	Revoke  bool
	Confirm bool
	DryRun  bool
}

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
		return output.WriteJSON(w, dryRunView{DryRun: true, OrderID: p.OrderID, Revoke: p.Revoke, Requires: output.NonNil(p.Requires)})
	}
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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	orderID := strings.TrimSpace(in.OrderID)
	if orderID == "" {
		return nil, exit.Usagef("no order: pass an order ID: gplay orders refund <orderId> --confirm")
	}

	requires := []string{"confirm"}

	if in.DryRun {
		return Payload{OrderID: orderID, Revoke: in.Revoke, Requires: requires, DryRun: true}, nil
	}

	if !in.Confirm {
		return nil, exit.SafetyFlag("confirm", "refunding order %q moves money and cannot be undone; pass --confirm to proceed (rehearse first with --dry-run)", orderID)
	}

	pkg, err := rc.Package(in.Package)
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

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "refund <orderId>",
		Short: "Refund a Google Play order (money-moving, irreversible)",
		Long: `Refund a single Google Play order by its order ID via orders.refund: a
money-moving, irreversible write. By default the money is returned but the
buyer keeps access to what they bought; pass --revoke to additionally terminate
the entitlement (for a subscription, all future payments stop too).

This is destructive, so it refuses without --confirm (exit 3, naming the flag);
CI=true never auto-confirms. Use --dry-run to preview the target with no HTTP
call: under --output json the preview lists the gate in a "requires" array.
GPLAY_READONLY refuses it (exit 4). There is no bulk refund: refund one order
at a time.

Refunding requires the service account to hold the CAN_MANAGE_ORDERS permission
(never part of a Role bundle); a 403 names it. Google does not allow refunding
orders older than 3 years: that surfaces as a specific refusal, not a generic
error.`,
		Example: `  # Preview the refund, no HTTP call
  gplay orders refund GPA.1234-5678-9012-34567 --dry-run

  # Refund and also revoke what the buyer bought
  gplay orders refund GPA.1234-5678-9012-34567 --revoke --confirm`,
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
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the refund (required: this moves money and is irreversible)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "preview the target without any HTTP call (reports the --confirm requirement)")
	return cmd
}
