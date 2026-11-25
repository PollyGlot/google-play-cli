package orders

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opListVoided = "purchases.voidedpurchases.list"

var mListVoided = apiregistry.MustResolve("androidpublisher.purchases.voidedpurchases.list")

const MaxVoidedLookback = 30 * 24 * time.Hour

type VoidedListOptions struct {
	Start                 time.Time
	IncludeSubscriptions  bool
	IncludePartialRefunds bool
	Limit                 int
}

type VoidedPurchase struct {
	OrderID            string `json:"orderId,omitempty"`
	PurchaseToken      string `json:"purchaseToken,omitempty"`
	PurchaseTimeMillis string `json:"purchaseTimeMillis,omitempty"`
	VoidedTimeMillis   string `json:"voidedTimeMillis,omitempty"`
	VoidedSource       *int   `json:"voidedSource,omitempty"`
	VoidedReason       *int   `json:"voidedReason,omitempty"`
	VoidedQuantity     *int   `json:"voidedQuantity,omitempty"`
}

type VoidedList struct {
	Purchases []VoidedPurchase
	Raw       json.RawMessage
}

func ListVoided(ctx context.Context, hc *http.Client, pkg string, opts VoidedListOptions) (VoidedList, bool, error) {
	base := url.Values{}
	base.Set("startTime", strconv.FormatInt(opts.Start.UnixMilli(), 10))
	if opts.IncludeSubscriptions {
		base.Set("type", "1")
	} else {
		base.Set("type", "0")
	}
	if opts.IncludePartialRefunds {
		base.Set("includeQuantityBasedPartialRefund", "true")
	}

	items, truncated, err := api.Paginate(api.Pager{Op: opListVoided, Target: pkg, What: opListVoided, Limit: opts.Limit},
		func(token string, _ int) ([]json.RawMessage, string, error) {
			q := url.Values{}
			for k, vs := range base {
				q[k] = append([]string(nil), vs...)
			}
			if token != "" {
				q.Set("token", token)
			}
			var page struct {
				VoidedPurchases []json.RawMessage `json:"voidedPurchases"`
				TokenPagination struct {
					NextPageToken string `json:"nextPageToken"`
				} `json:"tokenPagination"`
			}
			raw, err := api.Do(ctx, hc, api.Call{
				Method: mListVoided, Op: opListVoided, Target: pkg,
				Params: map[string]string{"packageName": pkg},
				Query:  q,
			})
			if err != nil {
				return nil, "", err
			}
			if len(raw) == 0 {
				return nil, "", nil
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return nil, "", api.DecodeFailure(api.Error{Operation: opListVoided, Package: pkg, Message: "decode response: " + err.Error(), Cause: err})
			}
			return page.VoidedPurchases, page.TokenPagination.NextPageToken, nil
		})
	if err != nil {
		return VoidedList{}, false, err
	}

	out := VoidedList{Purchases: make([]VoidedPurchase, 0, len(items))}
	for _, it := range items {
		var vp VoidedPurchase
		if err := json.Unmarshal(it, &vp); err != nil {
			return VoidedList{}, false, api.DecodeFailure(api.Error{Operation: opListVoided, Package: pkg, Message: "decode voided purchase: " + err.Error(), Cause: err})
		}
		out.Purchases = append(out.Purchases, vp)
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	if out.Raw, err = output.Marshal(map[string][]json.RawMessage{"voidedPurchases": items}); err != nil {
		return VoidedList{}, false, err
	}
	return out, truncated, nil
}
