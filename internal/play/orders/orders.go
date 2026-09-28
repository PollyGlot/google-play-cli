// Package orders reads Google Play Order resources via the Android Publisher
// orders endpoints. These are Edit-free, application-scoped reads on the package
// axis (applications/{packageName}/orders/...), the admin-side commerce
// diagnostic: a human or agent holds an order ID from a complaint or a payout
// report and looks it up: no device token, unlike runtime purchase-token
// verification (CONTEXT.md "Order", ADR-0031). Raw HTTP (ADR-0007), never the
// google-go-sdk.
//
// This package ships the full orders surface (PRD #245): orders.get (a single
// order, #282), orders.batchget (2–1000 orders in one call, #283), and the
// money-moving orders.refund (#284): all on the same package axis.
package orders

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// op* tag *api.Error with the REST reference method id.
const (
	opGet      = "orders.get"
	opBatchGet = "orders.batchget"
	opRefund   = "orders.refund"
)

// m* are the registry entries this package calls. Resolving them at init turns
// an unregistered or vanished method into a startup panic CI catches (the
// registry tests resolve every entry), never a runtime surprise for a user;
// verb and URL template then come from the Discovery snapshot instead of
// literals maintained here (#513). The two custom verbs
// (`orders:batchGet`, `{orderId}:refund`) are part of the snapshot's flatPath,
// so they ride the template rather than being concatenated here.
var (
	mGet      = apiregistry.MustResolve("androidpublisher.orders.get")
	mBatchGet = apiregistry.MustResolve("androidpublisher.orders.batchget")
	mRefund   = apiregistry.MustResolve("androidpublisher.orders.refund")
)

// MaxBatchOrderIDs is the API cap on orderIds per orders.batchget call (the
// Discovery snapshot states 1–1000 inclusive). The command surfaces an
// over-cap request as a usage error (exit 2) rather than letting the API reject
// it, so the limit is named locally.
const MaxBatchOrderIDs = 1000

// Money mirrors the Money schema: an amount split into whole units (a decimal
// int64 string) plus nano (10^-9) units, tagged with an ISO-4217 currency.
type Money struct {
	CurrencyCode string `json:"currencyCode,omitempty"`
	Units        string `json:"units,omitempty"`
	Nanos        int32  `json:"nanos,omitempty"`
}

// LineItem mirrors the subset of the LineItem schema the human summary reads:
// the product and its total. --output json passes the verbatim body through
// (ADR-0003), so this never has to be exhaustive.
type LineItem struct {
	ProductID    string `json:"productId,omitempty"`
	ProductTitle string `json:"productTitle,omitempty"`
	Total        *Money `json:"total,omitempty"`
}

// Order mirrors the subset of the Order schema the human summary reads (order
// id, state, total, creation time, line items). The complete resource (buyer
// address, tax, order history, points, sales channel, …) is always available
// verbatim via --output json (ADR-0003), so this stays intentionally small.
type Order struct {
	OrderID    string     `json:"orderId,omitempty"`
	State      string     `json:"state,omitempty"`
	Total      *Money     `json:"total,omitempty"`
	CreateTime string     `json:"createTime,omitempty"`
	LineItems  []LineItem `json:"lineItems,omitempty"`
}

// BatchGetOrdersResponse mirrors the orders.batchget envelope: a flat list of
// the requested Orders, in the API's order. The complete envelope is always
// available verbatim via --output json (ADR-0003).
type BatchGetOrdersResponse struct {
	Orders []Order `json:"orders,omitempty"`
}

// Get reads a single order by order ID via orders.get. It returns the parsed
// order and the verbatim body for the ADR-0003 --output json pass-through. No
// Edit: the GET is application-scoped (not under /edits/). Reading requires the
// service account to hold CAN_VIEW_FINANCIAL_DATA; a 403 surfaces as an
// *api.Error the command classifies into an agent-resolvable refusal.
func Get(ctx context.Context, hc *http.Client, pkg, orderID string) (Order, json.RawMessage, error) {
	var o Order
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mGet, Op: opGet, Target: pkg,
		Params: map[string]string{"packageName": pkg, "orderId": orderID},
	}, &o)
	if err != nil {
		return Order{}, nil, err
	}
	return o, raw, nil
}

// BatchGet reads several orders in one call via orders.batchget: the order IDs
// ride the repeated `orderIds` query parameter (the API caps the list at
// 1–1000 and requires distinct IDs; if any ID is unknown or belongs to another
// package the whole request fails). It returns the parsed envelope and the
// verbatim body for the ADR-0003 --output json pass-through. No Edit: the GET is
// application-scoped. Reading requires CAN_VIEW_FINANCIAL_DATA, like Get.
func BatchGet(ctx context.Context, hc *http.Client, pkg string, orderIDs []string) (BatchGetOrdersResponse, json.RawMessage, error) {
	q := url.Values{}
	for _, id := range orderIDs {
		q.Add("orderIds", id)
	}
	var resp BatchGetOrdersResponse
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mBatchGet, Op: opBatchGet, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Query:  q,
	}, &resp)
	if err != nil {
		return BatchGetOrdersResponse{}, nil, err
	}
	return resp, raw, nil
}

// Refund refunds a single order via orders.refund (POST, no body). When revoke
// is true the API additionally terminates the buyer's access (the `revoke`
// query parameter); the default (money back, entitlement kept) is the safe
// one (ADR-0031). The call is irreversible and money-moving, so the command
// gates it behind --confirm before reaching here. Refunding requires the
// service account to hold CAN_MANAGE_ORDERS (never part of a Role bundle); the
// API also rejects orders older than 3 years. Both surface as *api.Error the
// command classifies into agent-resolvable refusals. A success body is usually
// empty, so the verbatim bytes are returned for pass-through but may be nil.
func Refund(ctx context.Context, hc *http.Client, pkg, orderID string, revoke bool) (json.RawMessage, error) {
	var q url.Values
	if revoke {
		q = url.Values{"revoke": {"true"}}
	}
	return api.Do(ctx, hc, api.Call{
		Method: mRefund, Op: opRefund, Target: pkg,
		Params: map[string]string{"packageName": pkg, "orderId": orderID},
		Query:  q,
	})
}
