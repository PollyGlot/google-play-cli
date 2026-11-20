package apply_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/iotest"

	applycmd "github.com/PollyGlot/google-play-cli/commands/iap/apply"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const overLimit = 101

type batchRT struct {
	iapRT
	mu       sync.Mutex
	batches  []batchCall
	failCall int
	failWith func() *http.Response
}

type batchCall struct {
	verb string
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
	if n == r.failCall && r.failWith != nil {
		return r.failWith(), nil
	}
	if n == r.failCall && r.failWith != nil {
		return r.failWith(), nil
	}
	if n == r.failCall {
		return testkit.Response(http.StatusInternalServerError, `{"error":{"code":500,"message":"backend error","status":"INTERNAL"}}`), nil
	}
	return testkit.Response(http.StatusOK, `{}`), nil
}

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
	assertMessage(t, err, []string{
		"100 of 101",
		"applied: " + strings.Join(offerIDs[:100], ", "),
		"outcome unknown (Play may have applied them): o100;",
		"not sent: none",
		"coins100/buy",
	}, "not applied")
	assertChunks(t, rt.batches, "batchUpdate", offerIDs[:100], offerIDs[100:])
}

func offerDeletes201(t *testing.T, rt *batchRT) ([]string, applycmd.Input) {
	t.Helper()
	offerIDs := ids("o", 2*100+1)
	rt.offersBody = `{"oneTimeProductOffers":[` +
		joinObjects(len(offerIDs), `{"packageName":"com.example.app","productId":"coins100","purchaseOptionId":"buy","offerId":%q}`, offerIDs) + `]}`
	dir := writeCatalog(t, map[string]string{
		"coins100.json": `{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":[{"purchaseOptionId":"buy"}]}`,
		"old_gems.json": legacyFile,
	})
	return offerIDs, applycmd.Input{Package: "com.example.app", Dir: dir, Confirm: true}
}

func TestRun_chunkAcceptedButBodyUnreadable_isNotReportedNotApplied(t *testing.T) {
	rt := &batchRT{failCall: 2, failWith: func() *http.Response {
		resp := testkit.Response(http.StatusOK, "")
		resp.Body = io.NopCloser(iotest.ErrReader(errors.New("connection reset by peer")))
		return resp
	}}
	offerIDs, in := offerDeletes201(t, rt)
	_, err := applycmd.Run(newRCServing(t, rt.serve), in)
	if err == nil {
		t.Fatal("Run succeeded, want the second chunk's read failure")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusOK || apiErr.ExitCode() != 50 {
		t.Errorf("err %v must keep the API error (HTTP 200, exit 50) for the exit code and the envelope", err)
	}
	assertMessage(t, err, []string{
		"100 of 201",
		"applied: " + strings.Join(offerIDs[:100], ", ") + ";",
		"outcome unknown (Play may have applied them): " + strings.Join(offerIDs[100:200], ", ") + ";",
		"not sent: o200",
		"--dry-run",
	}, "not applied")
	assertChunks(t, rt.batches, "batchDelete", offerIDs[:100], offerIDs[100:200])
}

func TestRun_chunkRefused_isReportedNotApplied(t *testing.T) {
	rt := &batchRT{failCall: 2, failWith: func() *http.Response {
		return testkit.Response(http.StatusBadRequest, `{"error":{"code":400,"message":"invalid offer","status":"INVALID_ARGUMENT"}}`)
	}}
	offerIDs, in := offerDeletes201(t, rt)
	_, err := applycmd.Run(newRCServing(t, rt.serve), in)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("err %v must keep the API's 400 for the exit code and the envelope", err)
	}
	assertMessage(t, err, []string{
		"100 of 201",
		"applied: " + strings.Join(offerIDs[:100], ", ") + ";",
		"not applied: " + strings.Join(offerIDs[100:], ", ") + ")",
	}, "outcome unknown")
	assertChunks(t, rt.batches, "batchDelete", offerIDs[:100], offerIDs[100:200])
}

func assertMessage(t *testing.T, err error, want []string, absent string) {
	t.Helper()
	msg := err.Error()
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("error lacks %q:\n%s", w, msg)
		}
	}
	if strings.Contains(msg, absent) {
		t.Errorf("error must not say %q:\n%s", absent, msg)
	}
}
