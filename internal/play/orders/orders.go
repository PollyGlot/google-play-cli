// Package orders reads Google Play Order resources via the Android Publisher
// orders endpoints. These are Edit-free, application-scoped reads on the package
// axis (applications/{packageName}/orders/...), the admin-side commerce
// diagnostic: a human or agent holds an order ID from a complaint or a payout
// report and looks it up — no device token, unlike runtime purchase-token
// verification (CONTEXT.md "Order", ADR-0031). Raw HTTP (ADR-0007), never the
// google-go-sdk.
//
// This walking skeleton (#282) ships orders.get only — a single order by ID.
// orders.batchget (#283) and orders.refund (#284) follow on the same axis.
package orders

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// opGet tags *api.Error with the REST reference method id.
const opGet = "orders.get"

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
// id, state, total, creation time, line items). The complete resource — buyer
// address, tax, order history, points, sales channel, … — is always available
// verbatim via --output json (ADR-0003), so this stays intentionally small.
type Order struct {
	OrderID    string     `json:"orderId,omitempty"`
	State      string     `json:"state,omitempty"`
	Total      *Money     `json:"total,omitempty"`
	CreateTime string     `json:"createTime,omitempty"`
	LineItems  []LineItem `json:"lineItems,omitempty"`
}

// Get reads a single order by order ID via orders.get. It returns the parsed
// order and the verbatim body for the ADR-0003 --output json pass-through. No
// Edit: the GET is application-scoped (not under /edits/). Reading requires the
// service account to hold CAN_VIEW_FINANCIAL_DATA; a 403 surfaces as an
// *api.Error the command classifies into an agent-resolvable refusal.
func Get(ctx context.Context, hc *http.Client, pkg, orderID string) (Order, json.RawMessage, error) {
	u := api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) +
		"/orders/" + url.PathEscape(orderID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Order{}, nil, &api.Error{Operation: opGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	raw, err := do(hc, opGet, pkg, req)
	if err != nil {
		return Order{}, nil, err
	}
	var o Order
	if err := json.Unmarshal(raw, &o); err != nil {
		return Order{}, nil, &api.Error{Operation: opGet, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
	}
	return o, raw, nil
}

// do runs req and maps the response to (raw body, *api.Error): a non-2xx body is
// parsed for the error envelope, a 2xx body is returned verbatim for the
// ADR-0003 pass-through.
func do(hc *http.Client, op, pkg string, req *http.Request) (json.RawMessage, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(b, resp.StatusCode)
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "read response body: " + readErr.Error(), Cause: readErr}
	}
	return json.RawMessage(raw), nil
}
