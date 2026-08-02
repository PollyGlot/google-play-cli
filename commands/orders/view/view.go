// Package view implements `gplay orders view <orderId> [<orderId>...]`: a
// Google Play order lookup by order ID, end-to-end. One ID calls orders.get;
// two to 1000 IDs call orders.batchget — one ergonomic read verb hides the
// get-vs-batchget routing (ADR-0031). Read-only and Edit-free — a direct
// application-scoped GET on the package axis. The human views show a compact
// summary (single: order id, state, total, creation time, line items; batch:
// one summary line per order); --output json is the Order (single) /
// BatchGetOrdersResponse (batch) verbatim (ADR-0003 pass-through). Reading
// requires the service account to hold CAN_VIEW_FINANCIAL_DATA; a 403 surfaces
// as an agent-resolvable refusal naming it. Ships [experimental] (ADR-0010).
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

// Input is the request-shaped struct cobra builds from the variadic positional
// order IDs plus the --package flag.
type Input struct {
	Package  string
	OrderIDs []string
}

// Payload satisfies output.Renderable. Orders holds one order (single lookup)
// or several (batch); Batch records which API method answered so the renderers
// pick the detailed single view vs the one-line-per-order summary. Raw carries
// the verbatim bytes for the ADR-0003 JSON pass-through (Order for single,
// BatchGetOrdersResponse for batch).
type Payload struct {
	Orders []orders.Order
	Batch  bool
	Raw    json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

// renderTable picks the detailed single-order view or the batch summary.
func (p Payload) renderTable(w io.Writer) error {
	if p.Batch {
		return renderBatchTable(w, p.Orders)
	}
	return renderSingleTable(w, p.single())
}

// renderMarkdown picks the detailed single-order record or the batch list.
func (p Payload) renderMarkdown(w io.Writer) error {
	if p.Batch {
		return renderBatchMarkdown(w, p.Orders)
	}
	return renderSingleMarkdown(w, p.single())
}

// single returns the lone order for the single-lookup path; a zero Order if the
// slice is somehow empty (defensive — Run always populates one).
func (p Payload) single() orders.Order {
	if len(p.Orders) == 0 {
		return orders.Order{}
	}
	return p.Orders[0]
}

// headerRows is the scalar summary shared by the single table and markdown
// views, in a fixed order. Empty values are kept (an absent total reads as a
// blank cell, not a dropped row).
func headerRows(o orders.Order) [][2]string {
	return [][2]string{
		{"ORDER_ID", o.OrderID},
		{"STATE", o.State},
		{"TOTAL", formatMoney(o.Total)},
		{"CREATE_TIME", o.CreateTime},
	}
}

// renderSingleTable writes the summary as `FIELD<TAB>VALUE` lines (like
// `apps view`/`reviews view`), then one line per line item under a "Line items:"
// label.
func renderSingleTable(w io.Writer, o orders.Order) error {
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

// renderBatchTable writes one tab-separated summary line per order (id, state,
// total, create time) — a compact roster for the multi-ID lookup.
func renderBatchTable(w io.Writer, os []orders.Order) error {
	for _, o := range os {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", o.OrderID, o.State, formatMoney(o.Total), o.CreateTime); err != nil {
			return err
		}
	}
	return nil
}

// renderJSON emits the verbatim API bytes (ADR-0003 pass-through) — the Order
// for a single lookup, the BatchGetOrdersResponse for a batch. Raw is always
// populated on the Run path; an empty Raw would mean the API body was never
// captured, so we error rather than emit zero bytes.
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw orders payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderSingleMarkdown renders the order as a record: a level-2 heading then a
// `- **Field**: value` list (docs/DESIGN.md §7), followed by the line items as a
// bullet list, so a pasted report stands alone.
func renderSingleMarkdown(w io.Writer, o orders.Order) error {
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

// renderBatchMarkdown renders the batch as a heading + one bullet per order
// (id — state — total — create time), so a pasted multi-order report stands
// alone.
func renderBatchMarkdown(w io.Writer, os []orders.Order) error {
	if _, err := fmt.Fprintf(w, "## Orders (%d)\n\n", len(os)); err != nil {
		return err
	}
	for _, o := range os {
		parts := []string{o.OrderID, o.State, formatMoney(o.Total), o.CreateTime}
		kept := parts[:0]
		for _, p := range parts {
			if strings.TrimSpace(p) != "" {
				kept = append(kept, p)
			}
		}
		if _, err := fmt.Fprintf(w, "- %s\n", strings.Join(kept, " — ")); err != nil {
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

// trimIDs drops empty/whitespace-only order IDs (a stray "" from quoting) and
// trims the rest, preserving order.
func trimIDs(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// Run is the business function the kernel invokes. It validates the order IDs,
// resolves the package, builds an authenticated client, then routes: one ID to
// orders.get, two to MaxBatchOrderIDs to orders.batchget. Over the cap is a
// usage error (exit 2) naming the limit; a 404/403 is classified into an
// agent-resolvable refusal.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	ids := trimIDs(in.OrderIDs)
	if len(ids) == 0 {
		return nil, orderscmd.Usagef("no order — pass one or more order IDs: gplay orders view <orderId> [<orderId>...]")
	}
	if len(ids) > orders.MaxBatchOrderIDs {
		return nil, orderscmd.Usagef("too many order IDs (%d) — orders.batchget accepts between 1 and %d per request; split the list into smaller batches", len(ids), orders.MaxBatchOrderIDs)
	}
	pkg, err := orderscmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	if len(ids) == 1 {
		o, raw, err := orders.Get(rc.Ctx, httpClient, pkg, ids[0])
		if err != nil {
			return nil, orderscmd.ClassifyView(pkg, ids[0], err)
		}
		return Payload{Orders: []orders.Order{o}, Raw: raw}, nil
	}

	resp, raw, err := orders.BatchGet(rc.Ctx, httpClient, pkg, ids)
	if err != nil {
		return nil, orderscmd.ClassifyBatchView(pkg, len(ids), err)
	}
	return Payload{Orders: resp.Orders, Batch: true, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay orders view <orderId> [...]`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view <orderId> [<orderId>...]",
		Short: "Look up Google Play orders by order ID",
		Long: `Look up one or more Google Play orders by order ID — the admin-side commerce
diagnostic: a human or agent holds an order ID from a buyer complaint or a
payout report and reads its state, total, and line items. This is the order
lookup boundary of the commerce surface; real-time purchase-token verification
is a runtime API gplay does not wrap (CONTEXT.md "Order" / ADR-0031).

Pass a single order ID for a detailed lookup (orders.get) or several to look
them up together (orders.batchget, 1–1000 IDs per request — more is a usage
error). batchget is all-or-nothing: if any ID is unknown or belongs to another
package, the whole request fails. The order ID looks like
` + "`GPA.1234-5678-9012-34567`" + `. The package defaults to the repo's
.gplay/config.json pin when --package is omitted. This is a direct
application-scoped read — it opens no Edit.

The human views show a compact summary (single: order id, state, total,
creation time, line items; multiple: one line per order); --output json passes
the Order (single) or BatchGetOrdersResponse (batch) through verbatim
(ADR-0003), including the fields the summary omits (buyer address, tax, order
history, sales channel, …).

Reading orders requires the service account to hold the CAN_VIEW_FINANCIAL_DATA
permission (never part of a Role bundle); a 403 names it. An unknown order ID
fails with exit 30.`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.OrderIDs = args
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	return cmd
}
