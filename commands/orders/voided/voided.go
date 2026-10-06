// Package voided implements `gplay orders voided`: the purchases Google Play
// voided (refunded, charged back, revoked) over the last --since window, via
// purchases.voidedpurchases.list (#346). The reconciliation read next to
// `orders view` / `orders refund` (ADR-0031): pure read, no Edit, no
// GPLAY_READONLY gate. --output json is the API's voidedPurchases envelope,
// every page's items verbatim (ADR-0003); the human views decode the numeric
// source/reason codes and epoch-millis times. Ships [experimental] with the
// rest of `orders` (ADR-0010).
package voided

import (
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/orders/orderscmd"
	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/orders"
)

// DefaultSince is the widest window the API accepts, so the bare command reads
// everything it can.
const DefaultSince = "30d"

// Type values for --type.
const (
	TypeAll   = "all"
	TypeInapp = "inapp"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package               string
	Since                 string
	Type                  string
	IncludePartialRefunds bool
	Limit                 int
}

// Payload renders a voided-purchases walk.
type Payload struct {
	List orders.VoidedList
}

// voidedSources and voidedReasons name the API's integer codes, in the order
// the VoidedPurchase schema lists them.
var (
	voidedSources = []string{"user", "developer", "google"}
	voidedReasons = []string{"other", "remorse", "not_received", "defective", "accidental_purchase", "fraud", "friendly_fraud", "chargeback", "unacknowledged_purchase"}
)

// codeName names a code, falling back to the bare number for a code newer than
// this build, and to blank when the API omitted it.
func codeName(names []string, code *int) string {
	if code == nil {
		return ""
	}
	if *code >= 0 && *code < len(names) {
		return names[*code]
	}
	return strconv.Itoa(*code)
}

// millisTime renders an epoch-millis string as RFC3339 UTC; an unparsable
// value is shown as sent rather than dropped.
func millisTime(ms string) string {
	if ms == "" {
		return ""
	}
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return ms
	}
	return time.UnixMilli(n).UTC().Format(time.RFC3339)
}

func quantity(vp orders.VoidedPurchase) string {
	if vp.VoidedQuantity == nil {
		return ""
	}
	return strconv.Itoa(*vp.VoidedQuantity)
}

var columns = []output.Column[orders.VoidedPurchase]{
	{Key: "order_id", Header: "ORDER_ID", Value: func(v orders.VoidedPurchase) string { return v.OrderID }},
	{Key: "purchase_time", Header: "PURCHASE_TIME", Value: func(v orders.VoidedPurchase) string { return millisTime(v.PurchaseTimeMillis) }},
	{Key: "voided_time", Header: "VOIDED_TIME", Value: func(v orders.VoidedPurchase) string { return millisTime(v.VoidedTimeMillis) }},
	{Key: "voided_source", Header: "VOIDED_SOURCE", Value: func(v orders.VoidedPurchase) string { return codeName(voidedSources, v.VoidedSource) }},
	{Key: "voided_reason", Header: "VOIDED_REASON", Value: func(v orders.VoidedPurchase) string { return codeName(voidedReasons, v.VoidedReason) }},
	{Key: "voided_quantity", Header: "VOIDED_QUANTITY", Value: quantity},
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, columns, p.List.Purchases) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.List.Raw) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, columns, p.List.Purchases) },
	}
}

// Run validates the window, type and limit before any request (the API
// rejects a startTime older than 30 days with a 400; gplay names the limit as
// a usage error instead), then walks the list and warns on stderr when
// --limit hid items.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	spec := strings.TrimSpace(in.Since)
	if spec == "" {
		spec = DefaultSince
	}
	since, err := vitalscmd.ParseSince(spec)
	if err != nil {
		return nil, err
	}
	if since > orders.MaxVoidedLookback {
		return nil, exit.Usagef("--since %q reaches past 30 days: Google keeps voided purchases queryable for 30 days only; use 30d or less", in.Since)
	}
	var all bool
	switch strings.TrimSpace(in.Type) {
	case "", TypeAll:
		all = true
	case TypeInapp:
	default:
		return nil, exit.Usagef("invalid --type %q (want %s or %s)", in.Type, TypeAll, TypeInapp)
	}
	if in.Limit < 0 {
		return nil, exit.Usagef("invalid --limit %d (want 0 for all, or a positive cap)", in.Limit)
	}
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	list, truncated, err := orders.ListVoided(rc.Ctx, hc, pkg, orders.VoidedListOptions{
		Start:                 time.Now().Add(-since),
		IncludeSubscriptions:  all,
		IncludePartialRefunds: in.IncludePartialRefunds,
		Limit:                 in.Limit,
	})
	if err != nil {
		return nil, orderscmd.ClassifyList(pkg, err)
	}
	if truncated {
		rc.WarnTruncated(len(list.Purchases), "voided purchases", "limit")
	}
	return Payload{List: list}, nil
}

// NewCommand returns the cobra command for `gplay orders voided`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "voided",
		Short: "List voided purchases (refunds, chargebacks, revocations) for reconciliation",
		Long: `List the purchases Google Play voided for a package over a window: refunded,
charged back, or revoked, whether the buyer, the developer, or Google
initiated it. This is the reconciliation feed next to orders view and orders
refund: match each order ID against your own records to revoke an entitlement
or book a refund.

--since is the window back from now (default 30d, the widest Google allows:
a longer window is a usage error, exit 2). It filters on when Google recorded
the purchase as voided, not on the voided time shown. By default both in-app
products and subscriptions are listed; --type inapp restricts to in-app
products. --include-partial-refunds adds the quantity-based partial refunds of
multi-quantity purchases (VOIDED_QUANTITY carries the refunded quantity).

Every page is read unless --limit caps the count; a capped list warns on
stderr. Read-only, no Edit. --output json is the API's voidedPurchases list,
each item verbatim; the table decodes the source and reason codes and shows
times in UTC. Reading requires the service account to hold the
CAN_VIEW_FINANCIAL_DATA permission (never part of a Role bundle); a 403 names
it.`,
		Example: `  gplay orders voided --package com.example.app

  # Only in-app products voided in the last week, as JSON
  gplay orders voided --since 7d --type inapp --output json`,
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
	cmd.Flags().StringVar(&in.Since, "since", DefaultSince, "window length back from now, at most 30d, e.g. 7d or 48h")
	cmd.Flags().StringVar(&in.Type, "type", TypeAll, "purchases to list: all (in-app products and subscriptions) or inapp")
	cmd.Flags().BoolVar(&in.IncludePartialRefunds, "include-partial-refunds", false, "also list quantity-based partial refunds of multi-quantity purchases")
	cmd.Flags().IntVar(&in.Limit, "limit", 0, "max voided purchases to return (0 = all, no cap); a capped list warns on stderr")
	return cmd
}
