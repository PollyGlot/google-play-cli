package subscriptions_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/latency"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

// TestWrites_carryTheLatencyToleranceOnlyWhenSet drives every subscriptions
// apply write whose method takes latencyTolerance and reads it where
// Discovery puts it: the query of the patches, the body of the state verbs.
func TestWrites_carryTheLatencyToleranceOnlyWhenSet(t *testing.T) {
	ctx, pkg := context.Background(), "com.example.app"
	writes := []struct {
		name    string
		call    func(*http.Client, latency.Tolerance) error
		inQuery bool
	}{
		{"subscription patch", func(hc *http.Client, lt latency.Tolerance) error {
			_, err := subscriptions.Patch(ctx, hc, pkg, "premium", "2022/02", []string{"listings"}, json.RawMessage(`{"productId":"premium"}`), lt)
			return err
		}, true},
		{"offer patch", func(hc *http.Client, lt latency.Tolerance) error {
			_, err := subscriptions.PatchOffer(ctx, hc, pkg, "premium", "monthly", "intro", "2022/02", []string{"offerTags"}, json.RawMessage(`{"offerId":"intro"}`), lt)
			return err
		}, true},
		{"base plan activate", func(hc *http.Client, lt latency.Tolerance) error {
			_, err := subscriptions.SetBasePlanState(ctx, hc, pkg, "premium", "monthly", true, lt)
			return err
		}, false},
		{"offer deactivate", func(hc *http.Client, lt latency.Tolerance) error {
			_, err := subscriptions.SetOfferState(ctx, hc, pkg, "premium", "monthly", "intro", false, lt)
			return err
		}, false},
	}
	for _, w := range writes {
		for _, lt := range []latency.Tolerance{"", latency.Tolerant} {
			t.Run(w.name+"/"+string(lt), func(t *testing.T) {
				rt := subsFake(nil)
				if err := w.call(client(rt), lt); err != nil {
					t.Fatalf("%s: %v", w.name, err)
				}
				c := rt.Calls()[len(rt.Calls())-1]
				if lt == "" {
					if strings.Contains(c.Query+string(c.Body), "latencyTolerance") {
						t.Errorf("latencyTolerance sent without a tolerance: query %q body %s", c.Query, c.Body)
					}
					return
				}
				var got string
				if w.inQuery {
					q, err := url.ParseQuery(c.Query)
					if err != nil {
						t.Fatalf("parse query: %v", err)
					}
					got = q.Get("latencyTolerance")
				} else {
					var body struct {
						LatencyTolerance string `json:"latencyTolerance"`
					}
					if err := json.Unmarshal(c.Body, &body); err != nil {
						t.Fatalf("decode body: %v", err)
					}
					got = body.LatencyTolerance
				}
				if got != string(lt) {
					t.Errorf("latencyTolerance = %q, want %s (query %q body %s)", got, lt, c.Query, c.Body)
				}
			})
		}
	}
}
