// Package view implements `gplay orders view <orderId>`: a single Google Play
// order lookup by order ID, end-to-end (orders.get). Read-only and Edit-free — a
// direct application-scoped GET on the package axis (ADR-0031). The human views
// show a compact summary (order id, state, total, creation time, line items);
// --output json is the Order resource verbatim (ADR-0003 pass-through). Reading
// requires the service account to hold CAN_VIEW_FINANCIAL_DATA; a 403 surfaces
// as an agent-resolvable refusal naming it. Ships [experimental] (ADR-0010).
//
// This is the walking skeleton for the `orders` namespace (#282); the batch form
// `orders view <id> <id>...` (orders.batchget) follows in #283.
package view

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/orders/orderscmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/orders"
)

// Input is the request-shaped struct cobra builds from the positional orderId
// plus the --package flag.
type Input struct {
	Package string
	OrderID string
}

// Payload satisfies output.Renderable. Raw carries the Order's verbatim bytes
// for the ADR-0003 JSON pass-through; Order drives the human-shaped table and
// markdown views.
type Payload struct {
	Order orders.Order
	Raw   json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Order) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Order) },
	}
}

// headerRows is the scalar summary shared by the table and markdown views, in a
// fixed order. Empty values are kept (an absent total reads as a blank cell, not
// a dropped row).
func headerRows(o orders.Order) [][2]string {
	return [][2]string{
		{"ORDER_ID", o.OrderID},
		{"STATE", o.State},
		{"TOTAL", formatMoney(o.Total)},
		{"CREATE_TIME", o.CreateTime},
	}
}

// renderTable writes the summary as `FIELD<TAB>VALUE` lines (like
// `apps view`/`reviews view`), then one line per line item under a "Line items:"
// label.
func renderTable(w io.Writer, o orders.Order) error {
	for _, row := range headerRows(o) {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	if len(o.LineItems) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\nLine items:\n"); err != nil {
		return err
	}
	for _, li := range o.LineItems {
		if _, err := fmt.Fprintf(w, "  %s\n", lineItemSummary(li)); err != nil {
			return err
		}
	}
	return nil
}

// renderJSON emits the Order's bytes verbatim (ADR-0003 pass-through). Raw is
// always populated on the Run path; an empty Raw would mean the API body was
// never captured, so we error rather than emit zero bytes.
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw orders.get payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderMarkdown renders the order as a record: a level-2 heading then a
// `- **Field**: value` list (docs/DESIGN.md §7), followed by the line items as a
// bullet list, so a pasted report stands alone.
func renderMarkdown(w io.Writer, o orders.Order) error {
	heading := "Order"
	if o.OrderID != "" {
		heading = "Order " + o.OrderID
	}
	if _, err := fmt.Fprintf(w, "## %s\n\n", heading); err != nil {
		return err
	}
	// Skip ORDER_ID in the list (it is the heading); show the rest.
	for _, row := range headerRows(o)[1:] {
		if _, err := fmt.Fprintf(w, "- **%s**: %s\n", mdLabel(row[0]), row[1]); err != nil {
			return err
		}
	}
	if len(o.LineItems) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n**Line items**\n\n"); err != nil {
		return err
	}
	for _, li := range o.LineItems {
		if _, err := fmt.Fprintf(w, "- %s\n", lineItemSummary(li)); err != nil {
			return err
		}
	}
	return nil
}

// lineItemSummary renders one line item compactly: "productId (title) — total",
// dropping the parts that are absent.
func lineItemSummary(li orders.LineItem) string {
	label := li.ProductID
	if li.ProductTitle != "" {
		if label != "" {
			label += " (" + li.ProductTitle + ")"
		} else {
			label = li.ProductTitle
		}
	}
	if amount := formatMoney(li.Total); amount != "" {
		if label != "" {
			return label + " — " + amount
		}
		return amount
	}
	return label
}

// formatMoney renders a Money as "<amount> <CURRENCY>" (e.g. "4.99 USD"),
// combining the whole units with the nano fractional part. A nil Money is "".
func formatMoney(m *orders.Money) string {
	if m == nil {
		return ""
	}
	units := strings.TrimSpace(m.Units)
	if units == "" {
		units = "0"
	}
	amount := units
	if m.Nanos != 0 {
		nanos := m.Nanos
		sign := ""
		if nanos < 0 {
			nanos = -nanos
			// units carries the sign when it is non-zero; only add a leading
			// "-" for the units==0, negative-nanos case (e.g. -0.75).
			if !strings.HasPrefix(units, "-") {
				sign = "-"
			}
		}
		frac := strings.TrimRight(fmt.Sprintf("%09d", nanos), "0")
		amount = sign + units + "." + frac
	}
	if m.CurrencyCode != "" {
		return amount + " " + m.CurrencyCode
	}
	return amount
}

// mdLabel turns a SCREAMING_SNAKE header key into a "Title case" markdown label
// (CREATE_TIME → "Create time").
func mdLabel(key string) string {
	words := strings.Split(strings.ToLower(key), "_")
	if len(words) > 0 && words[0] != "" {
		words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	}
	return strings.Join(words, " ")
}

// Run is the business function the kernel invokes. It validates the orderId,
// resolves the package, builds an authenticated client, fetches the single order
// (orders.get), and renders. A 404/403 is classified into an agent-resolvable
// refusal (orderscmd.ClassifyView).
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	orderID := strings.TrimSpace(in.OrderID)
	if orderID == "" {
		return nil, orderscmd.Usagef("no order — pass an order ID: gplay orders view <orderId>")
	}
	pkg, err := orderscmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	o, raw, err := orders.Get(rc.Ctx, httpClient, pkg, orderID)
	if err != nil {
		return nil, orderscmd.ClassifyView(pkg, orderID, err)
	}
	return Payload{Order: o, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay orders view <orderId>`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view <orderId>",
		Short: "[experimental] Look up a Google Play order by its order ID",
		Long: `Look up a single Google Play order by its order ID — the admin-side commerce
diagnostic: a human or agent holds an order ID from a buyer complaint or a
payout report and reads its state, total, and line items. This is the order
lookup boundary of the commerce surface; real-time purchase-token verification
is a runtime API gplay does not wrap (CONTEXT.md "Order" / ADR-0031).

The order ID looks like ` + "`GPA.1234-5678-9012-34567`" + `. The package defaults to the
repo's .gplay/config.json pin when --package is omitted. This is a direct
application-scoped read — it opens no Edit.

The human views show a compact summary (order id, state, total, creation time,
line items); --output json passes the Order resource through verbatim
(ADR-0003), including the fields the summary omits (buyer address, tax, order
history, sales channel, …).

Reading orders requires the service account to hold the CAN_VIEW_FINANCIAL_DATA
permission (never part of a Role bundle); a 403 names it. An unknown order ID
fails with exit 30.

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
	return cmd
}
