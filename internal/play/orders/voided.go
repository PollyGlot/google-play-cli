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

// MaxVoidedLookback is how far back voidedpurchases.list accepts a startTime
// (Discovery: "cannot be older than 30 days"). The command refuses a longer
// window before any request rather than relaying the API's 400.
const MaxVoidedLookback = 30 * 24 * time.Hour

// VoidedListOptions shapes one voidedpurchases.list walk. Start is the oldest
// voided record to return (the API filters on when its systems saw the record
// as voided, not on voidedTimeMillis); the end is left to the API default
// (now). IncludeSubscriptions maps to type=1, otherwise type=0 (in-app
// products only). Limit caps the total item count, 0 = every page.
type VoidedListOptions struct {
	Start                 time.Time
	IncludeSubscriptions  bool
	IncludePartialRefunds bool
	Limit                 int
}

// VoidedPurchase is the subset of the VoidedPurchase schema the human views
// read. The int fields are pointers so an absent code renders blank instead of
// as code 0 (which means "user" / "other"). The complete item is always in
// VoidedList.Raw (ADR-0003).
type VoidedPurchase struct {
	OrderID            string `json:"orderId,omitempty"`
	PurchaseToken      string `json:"purchaseToken,omitempty"`
	PurchaseTimeMillis string `json:"purchaseTimeMillis,omitempty"`
	VoidedTimeMillis   string `json:"voidedTimeMillis,omitempty"`
	VoidedSource       *int   `json:"voidedSource,omitempty"`
	VoidedReason       *int   `json:"voidedReason,omitempty"`
	VoidedQuantity     *int   `json:"voidedQuantity,omitempty"`
}

// VoidedList is a completed walk: the parsed items for the human views and
// Raw, the {"voidedPurchases":[...]} envelope rebuilt from the verbatim item
// objects of every page (the convention of the other auto-paginated reads:
// per-page pageInfo/tokenPagination are walk state, not data).
type VoidedList struct {
	Purchases []VoidedPurchase
	Raw       json.RawMessage
}

// ListVoided lists the purchases voided (refunded, charged back, revoked) for
// pkg via purchases.voidedpurchases.list, following
// tokenPagination.nextPageToken (carried back as `token`) until exhausted or
// opts.Limit items are held. truncated reports that the limit hid items; the
// command turns it into the stderr warning. No Edit: an application-scoped
// GET. Reading requires CAN_VIEW_FINANCIAL_DATA, like the other order reads.
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
			// An empty body (a window with nothing voided) is an empty last
			// page, not a decode error.
			if len(raw) == 0 {
				return nil, "", nil
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return nil, "", &api.Error{Operation: opListVoided, Package: pkg, StatusCode: http.StatusOK, Message: "decode response: " + err.Error(), Cause: err}
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
			return VoidedList{}, false, &api.Error{Operation: opListVoided, Package: pkg, StatusCode: http.StatusOK, Message: "decode voided purchase: " + err.Error(), Cause: err}
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
