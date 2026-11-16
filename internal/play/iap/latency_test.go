package iap_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/iap"
	"github.com/PollyGlot/google-play-cli/internal/play/latency"
)

// latencyAt reads latencyTolerance from the query or from the JSON body at
// path (object keys and array indexes); nil when it is absent.
type latencyAt func(q url.Values, body any) any

func fromQuery(q url.Values, _ any) any {
	if v, ok := q["latencyTolerance"]; ok {
		return v[0]
	}
	return nil
}

func fromBody(path ...string) latencyAt {
	return func(_ url.Values, body any) any {
		for _, k := range path {
			switch n := body.(type) {
			case map[string]any:
				body = n[k]
			case []any:
				i, _ := strconv.Atoi(k)
				body = n[i]
			}
		}
		if m, ok := body.(map[string]any); ok {
			return m["latencyTolerance"]
		}
		return nil
	}
}

// TestWrites_carryTheLatencyToleranceOnlyWhenSet drives every write iap apply
// emits and reads latencyTolerance where Discovery puts it: the query of the
// patch and the delete, the body of the batch entries and the state verbs.
func TestWrites_carryTheLatencyToleranceOnlyWhenSet(t *testing.T) {
	ctx, pkg := context.Background(), "com.example.app"
	writes := []struct {
		name string
		call func(*http.Client, latency.Tolerance) error
		at   latencyAt
	}{
		{"patch", func(hc *http.Client, lt latency.Tolerance) error {
			_, err := iap.PatchOneTimeProduct(ctx, hc, pkg, "coins", "2022/02", []string{"listings"}, false, json.RawMessage(`{"productId":"coins"}`), lt)
			return err
		}, fromQuery},
		{"delete", func(hc *http.Client, lt latency.Tolerance) error {
			return iap.DeleteOneTimeProduct(ctx, hc, pkg, "coins", lt)
		}, fromQuery},
		{"offers batchUpdate", func(hc *http.Client, lt latency.Tolerance) error {
			return iap.BatchUpdateOffers(ctx, hc, pkg, "coins", "buy", []iap.OfferUpdate{{Offer: json.RawMessage(`{"offerId":"promo"}`), AllowMissing: true}}, "2022/02", lt)
		}, fromBody("requests", "0")},
		{"offers batchDelete", func(hc *http.Client, lt latency.Tolerance) error {
			return iap.BatchDeleteOffers(ctx, hc, pkg, "coins", "buy", []string{"promo"}, lt)
		}, fromBody("requests", "0")},
		{"purchaseOptions batchUpdateStates", func(hc *http.Client, lt latency.Tolerance) error {
			return iap.BatchUpdatePurchaseOptionStates(ctx, hc, pkg, "coins", []iap.PurchaseOptionStateUpdate{{PurchaseOptionID: "buy", Activate: true}}, lt)
		}, fromBody("requests", "0", "activatePurchaseOptionRequest")},
		{"offer activate", func(hc *http.Client, lt latency.Tolerance) error {
			_, err := iap.SetOfferState(ctx, hc, pkg, "coins", "buy", "promo", iap.StateActive, lt)
			return err
		}, fromBody()},
		{"offer cancel", func(hc *http.Client, lt latency.Tolerance) error {
			_, err := iap.SetOfferState(ctx, hc, pkg, "coins", "buy", "promo", iap.StateCancelled, lt)
			return err
		}, fromBody()},
	}
	for _, w := range writes {
		for _, lt := range []latency.Tolerance{"", latency.Tolerant} {
			t.Run(w.name+"/"+string(lt), func(t *testing.T) {
				rt := iapFake(nil)
				if err := w.call(client(rt), lt); err != nil {
					t.Fatalf("%s: %v", w.name, err)
				}
				c := rt.Calls()[len(rt.Calls())-1]
				q, err := url.ParseQuery(c.Query)
				if err != nil {
					t.Fatalf("parse query: %v", err)
				}
				var body any
				if len(c.Body) > 0 {
					if err := json.Unmarshal(c.Body, &body); err != nil {
						t.Fatalf("decode body: %v", err)
					}
				}
				got := w.at(q, body)
				if lt == "" {
					if strings.Contains(c.Query+string(c.Body), "latencyTolerance") {
						t.Errorf("latencyTolerance sent without a tolerance: query %q body %s", c.Query, c.Body)
					}
					return
				}
				if got != string(lt) {
					t.Errorf("latencyTolerance = %v, want %s (query %q body %s)", got, lt, c.Query, c.Body)
				}
			})
		}
	}
}
