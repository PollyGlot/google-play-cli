package apply_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	applycmd "github.com/PollyGlot/google-play-cli/commands/iap/apply"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// overLimit is one change more than the 100 requests Discovery allows in each
// one-time-product batch body (#807).
const overLimit = 101

// batchRT serves the live catalog of iapRT and records the identities each
// batch call carries, in call order. failCall, when set, answers that batch
// call (1-based) with a 500.
type batchRT struct {
	iapRT
	mu       sync.Mutex
	batches  []batchCall
	failCall int
}

type batchCall struct {
	verb string // the custom verb: batchUpdate, batchDelete, batchUpdateStates
	ids  []string
}

func (r *batchRT) serve(req *http.Request) (*http.Response, error) {
	_, verb, isBatch := strings.Cut(req.URL.Path, ":batch")
	if !isBatch || req.Method != http.MethodPost {
		return r.iapRT.serve(req)
	}
	var body struct {
		Requests []json.RawMessage `json:"requests"`
	}
	if err := json.Unmarshal(testkit.ReadBody(req), &body); err != nil {
		return nil, err
	}
	call := batchCall{verb: "batch" + verb}
	for _, raw := range body.Requests {
		var id struct {
			OfferID    string                            `json:"offerId"`
			Offer      struct{ OfferID string }          `json:"oneTimeProductOffer"`
			Activate   struct{ PurchaseOptionID string } `json:"activatePurchaseOptionRequest"`
			Deactivate struct{ PurchaseOptionID string } `json:"deactivatePurchaseOptionRequest"`
		}
		if err := json.Unmarshal(raw, &id); err != nil {
			return nil, err
		}
		call.ids = append(call.ids, id.OfferID+id.Offer.OfferID+id.Activate.PurchaseOptionID+id.Deactivate.PurchaseOptionID)
	}
	r.mu.Lock()
	r.batches = append(r.batches, call)
	n := len(r.batches)
	r.mu.Unlock()
	if n == r.failCall {
		return testkit.Response(http.StatusInternalServerError, `{"error":{"code":500,"message":"backend error","status":"INTERNAL"}}`), nil
	}
	return testkit.Response(http.StatusOK, `{}`), nil
}

// ids returns n identifiers that sort in creation order: o000, o001, ...
func ids(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%03d", prefix, i)
	}
	return out
}

func joinObjects(n int, format string, idList []string) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf(format, idList[i])
	}
	return strings.Join(parts, ",")
}

const legacyFile = `{"sku":"old_gems","purchaseType":"managedUser","status":"active"}`

// assertChunks checks the batch calls apply sent: verb, then the identities
// of each call, in order.
func assertChunks(t *testing.T, got []batchCall, verb string, want ...[]string) {
	t.Helper()
	if len(got) != len(want) {
		sizes := make([]int, len(got))
		for i, c := range got {
			sizes[i] = len(c.ids)
		}
		t.Fatalf("sent %d %s call(s) of sizes %v, want %d", len(got), verb, sizes, len(want))
	}
	for i, c := range got {
		if c.verb != verb {
			t.Errorf("call %d is %s, want %s", i+1, c.verb, verb)
		}
		if strings.Join(c.ids, ",") != strings.Join(want[i], ",") {
			t.Errorf("call %d carries %d request(s) %v, want %d %v", i+1, len(c.ids), c.ids, len(want[i]), want[i])
		}
	}
}

// offerCreatesCatalog declares 101 new offers under coins100/buy, none live.
func offerCreatesCatalog(t *testing.T, offerIDs []string) string {
	t.Helper()
	return writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy","offers":[` +
			joinObjects(len(offerIDs), `{"offerId":%q}`, offerIDs) + `]}]}`,
		"old_gems.json": legacyFile,
	})
}

func TestRun_offerUpdates_splitAtTheBatchLimit(t *testing.T) {
	offerIDs := ids("o", overLimit)
	rt := &batchRT{iapRT: iapRT{offersBody: `{}`}}
	rc := newRCServing(t, rt.serve)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: offerCreatesCatalog(t, offerIDs)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertChunks(t, rt.batches, "batchUpdate", offerIDs[:100], offerIDs[100:])
}

func TestRun_purchaseOptionStates_splitAtTheBatchLimit(t *testing.T) {
	optionIDs := ids("buy", overLimit)
	live := `{"oneTimeProducts":[{"productId":"coins100","packageName":"com.example.app","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[` +
		joinObjects(overLimit, `{"purchaseOptionId":%q,"state":"INACTIVE"}`, optionIDs) + `]}]}`
	dir := writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[` +
			joinObjects(overLimit, `{"purchaseOptionId":%q,"state":"ACTIVE"}`, optionIDs) + `]}`,
		"old_gems.json": legacyFile,
	})
	rt := &batchRT{iapRT: iapRT{v2Body: live, offersBody: `{}`}}
	rc := newRCServing(t, rt.serve)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertChunks(t, rt.batches, "batchUpdateStates", optionIDs[:100], optionIDs[100:])
}

func TestRun_offerDeletes_splitAtTheBatchLimit(t *testing.T) {
	offerIDs := ids("o", overLimit)
	liveOffers := `{"oneTimeProductOffers":[` +
		joinObjects(overLimit, `{"packageName":"com.example.app","productId":"coins100","purchaseOptionId":"buy","offerId":%q}`, offerIDs) + `]}`
	dir := writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy"}]}`,
		"old_gems.json": legacyFile,
	})
	rt := &batchRT{iapRT: iapRT{offersBody: liveOffers}}
	rc := newRCServing(t, rt.serve)
	if _, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertChunks(t, rt.batches, "batchDelete", offerIDs[:100], offerIDs[100:])
}

// A failing second chunk leaves the first one applied on Play: the error says
// which offers went through and which did not, and keeps the API's failure.
func TestRun_failedChunk_reportsWhatWasApplied(t *testing.T) {
	offerIDs := ids("o", overLimit)
	rt := &batchRT{iapRT: iapRT{offersBody: `{}`}, failCall: 2}
	rc := newRCServing(t, rt.serve)
	_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: offerCreatesCatalog(t, offerIDs)})
	if err == nil {
		t.Fatal("Run succeeded, want the second chunk's failure")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("err %v must keep the API's 500 for the exit code and the envelope", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"100 of 101",
		"applied: " + strings.Join(offerIDs[:100], ", "),
		"not applied: o100",
		"coins100/buy",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
	assertChunks(t, rt.batches, "batchUpdate", offerIDs[:100], offerIDs[100:])
}
