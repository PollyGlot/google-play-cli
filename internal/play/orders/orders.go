package orders

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opGet      = "orders.get"
	opBatchGet = "orders.batchget"
	opRefund   = "orders.refund"
)

var (
	mGet      = apiregistry.MustResolve("androidpublisher.orders.get")
	mBatchGet = apiregistry.MustResolve("androidpublisher.orders.batchget")
	mRefund   = apiregistry.MustResolve("androidpublisher.orders.refund")
)

const MaxBatchOrderIDs = 1000

type Money struct {
	CurrencyCode string `json:"currencyCode,omitempty"`
	Units        string `json:"units,omitempty"`
	Nanos        int32  `json:"nanos,omitempty"`
}

type LineItem struct {
	ProductID    string `json:"productId,omitempty"`
	ProductTitle string `json:"productTitle,omitempty"`
	Total        *Money `json:"total,omitempty"`
}

type Order struct {
	OrderID    string     `json:"orderId,omitempty"`
	State      string     `json:"state,omitempty"`
	Total      *Money     `json:"total,omitempty"`
	CreateTime string     `json:"createTime,omitempty"`
	LineItems  []LineItem `json:"lineItems,omitempty"`
}

type BatchGetOrdersResponse struct {
	Orders []Order `json:"orders,omitempty"`
}

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
