package testkit_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const (
	tracksUpdateURL  = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/edits/e1/tracks/production"
	subsCreateURL    = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/subscriptions?productId=p"
	offerCreateURL   = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/subscriptions/p/basePlans/m/offers"
	otpOffersURL     = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/oneTimeProducts/p/purchaseOptions/o/offers:batchUpdate"
	otpPatchURL      = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/onetimeproducts/p"
	bundleUploadURL  = "https://androidpublisher.googleapis.com/upload/androidpublisher/v3/applications/com.example.app/edits/e1/bundles"
	unregisteredHost = "https://api.test/applications/com.example.app/edits/e1/tracks/production"
)

// sendRecovering sends one request through rt and returns the panic it
// raised, "" when the round trip completed.
func sendRecovering(t *testing.T, rt http.RoundTripper, method, url, contentType, body string) (panicked string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			panicked = fmt.Sprint(r)
		}
	}()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	return ""
}

func TestFake_refusesBodiesPlayRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, method, url, body string
		broken                  string // the rule key the Fake must name, "" for a body Play accepts
	}{
		{"completed with a fraction (#731)", http.MethodPut, tracksUpdateURL, `{"releases":[{"status":"completed","userFraction":1}]}`, "TrackRelease.userFraction"},
		{"draft with a fraction", http.MethodPut, tracksUpdateURL, `{"releases":[{"status":"draft","userFraction":0.5}]}`, "TrackRelease.userFraction"},
		{"inProgress at a full fraction", http.MethodPut, tracksUpdateURL, `{"releases":[{"status":"inProgress","userFraction":1}]}`, "TrackRelease.userFraction"},
		{"inProgress at zero", http.MethodPut, tracksUpdateURL, `{"releases":[{"status":"inProgress","userFraction":0}]}`, "TrackRelease.userFraction"},
		{"second release broken", http.MethodPut, tracksUpdateURL, `{"releases":[{"status":"inProgress","userFraction":0.1},{"status":"completed","userFraction":1}]}`, "TrackRelease.userFraction"},
		{"completed without a fraction", http.MethodPut, tracksUpdateURL, `{"releases":[{"status":"completed"}]}`, ""},
		{"null fraction", http.MethodPut, tracksUpdateURL, `{"releases":[{"status":"completed","userFraction":null}]}`, ""},
		{"inProgress staged", http.MethodPut, tracksUpdateURL, `{"releases":[{"status":"inProgress","userFraction":0.1}]}`, ""},
		{"halted staged", http.MethodPut, tracksUpdateURL, `{"releases":[{"status":"halted","userFraction":0.5}]}`, ""},

		{"new subscribers without a price", http.MethodPost, subsCreateURL, `{"basePlans":[{"regionalConfigs":[{"regionCode":"FR","newSubscriberAvailability":true}]}]}`, "RegionalBasePlanConfig.price"},
		{"new subscribers with a price", http.MethodPost, subsCreateURL, `{"basePlans":[{"regionalConfigs":[{"regionCode":"FR","newSubscriberAvailability":true,"price":{"currencyCode":"EUR","units":"4"}}]}]}`, ""},
		{"closed region without a price", http.MethodPost, subsCreateURL, `{"basePlans":[{"regionalConfigs":[{"regionCode":"FR"}]}]}`, ""},
		{"age rating outside the US", http.MethodPost, subsCreateURL, `{"taxAndComplianceSettings":{"regionalProductAgeRatingInfos":[{"regionCode":"FR"}]}}`, "SubscriptionTaxAndComplianceSettings.regionalProductAgeRatingInfos"},
		{"age rating in the US", http.MethodPost, subsCreateURL, `{"taxAndComplianceSettings":{"regionalProductAgeRatingInfos":[{"regionCode":"US"}]}}`, ""},
		{"one-time product age rating outside the US", http.MethodPatch, otpPatchURL, `{"taxAndComplianceSettings":{"regionalProductAgeRatingInfos":[{"regionCode":"DE"}]}}`, "OneTimeProductTaxAndComplianceSettings.regionalProductAgeRatingInfos"},

		{"regional discount of 100%", http.MethodPost, offerCreateURL, `{"phases":[{"regionalConfigs":[{"regionCode":"FR","relativeDiscount":1}]}]}`, "RegionalSubscriptionOfferPhaseConfig.relativeDiscount"},
		{"other-regions discount of 0%", http.MethodPost, offerCreateURL, `{"phases":[{"otherRegionsConfig":{"relativeDiscount":0}}]}`, "OtherRegionsSubscriptionOfferPhaseConfig.relativeDiscount"},
		{"discount as a string", http.MethodPost, offerCreateURL, `{"phases":[{"regionalConfigs":[{"regionCode":"FR","relativeDiscount":"0.8"}]}]}`, ""},
		{"one-time offer discount above 1", http.MethodPost, otpOffersURL, `{"requests":[{"oneTimeProductOffer":{"regionalPricingAndAvailabilityConfigs":[{"regionCode":"FR","relativeDiscount":1.5}]}}]}`, "OneTimeProductOfferRegionalPricingAndAvailabilityConfig.relativeDiscount"},
		{"one-time offer discount", http.MethodPost, otpOffersURL, `{"requests":[{"oneTimeProductOffer":{"regionalPricingAndAvailabilityConfigs":[{"regionCode":"FR","relativeDiscount":0.2}]}}]}`, ""},

		{"media upload", http.MethodPost, bundleUploadURL, "PK\x03\x04", ""},
		{"no Discovery method at that URL", http.MethodPut, unregisteredHost, `{"releases":[{"status":"completed","userFraction":1}]}`, ""},
		{"wrong verb for the URL", http.MethodPost, tracksUpdateURL, `{"releases":[{"status":"completed","userFraction":1}]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
			got := sendRecovering(t, fake, tc.method, tc.url, "application/json", tc.body)
			switch {
			case tc.broken == "" && got != "":
				t.Fatalf("a body Play accepts was refused:\n%s", got)
			case tc.broken != "" && !strings.Contains(got, tc.broken+" at "):
				t.Fatalf("want a refusal naming %s, got %q", tc.broken, got)
			}
			if n := len(fake.Calls()); n != 1 {
				t.Errorf("recorded %d calls, want the one sent", n)
			}
		})
	}
}

// The failure a test author reads names the request, where the property sits
// in the body, the sentence Google wrote and the way out for a deliberate 400.
func TestFake_refusalMessage(t *testing.T) {
	got := sendRecovering(t, testkit.NewFake(testkit.Any(http.StatusOK, `{}`)), http.MethodPut, tracksUpdateURL, "application/json",
		`{"releases":[{"status":"inProgress","userFraction":0.1},{"status":"completed","userFraction":1}]}`)
	for _, want := range []string{
		"androidpublisher.edits.tracks.update",
		"PUT " + tracksUpdateURL,
		`TrackRelease.userFraction at releases[1]: userFraction 1 set on a release whose status is "completed", not inProgress or halted`,
		`Discovery: "0 < fraction < 1. Can only be set when status is \"inProgress\" or \"halted\"."`,
		`AllowInvalid("TrackRelease.userFraction")`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal lacks %q:\n%s", want, got)
		}
	}
}

func TestFake_AllowInvalid_letsADeliberate400Through(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusBadRequest, `{"error":{"code":400}}`)).AllowInvalid("TrackRelease.userFraction")
	if got := sendRecovering(t, fake, http.MethodPut, tracksUpdateURL, "application/json", `{"releases":[{"status":"completed","userFraction":1}]}`); got != "" {
		t.Fatalf("an exempted rule still refused:\n%s", got)
	}
	got := sendRecovering(t, fake, http.MethodPost, subsCreateURL, "application/json", `{"basePlans":[{"regionalConfigs":[{"newSubscriberAvailability":true}]}]}`)
	if !strings.Contains(got, "RegionalBasePlanConfig.price") {
		t.Errorf("exempting one rule must leave the others on, got %q", got)
	}
}

func TestFake_AllowInvalid_refusesAnUnknownRule(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), `AllowInvalid("TrackRelease.fraction"): no such rule`) {
			t.Errorf("panic = %v, want the unknown rule named", r)
		}
	}()
	testkit.NewFake().AllowInvalid("TrackRelease.fraction")
}

// recordingTB stands in for the test a Wire fails, so the failure itself can
// be asserted.
type recordingTB struct {
	testing.TB
	mu   sync.Mutex
	errs []string
}

func (r *recordingTB) Error(args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, fmt.Sprint(args...))
}

func TestWire_failsTheTestOnABodyPlayRefuses(t *testing.T) {
	tb := &recordingTB{TB: t}
	w := testkit.NewWire(tb, testkit.Any(http.StatusOK, `{}`))
	status, body, err := send(t, w.Client(), http.MethodPut, tracksUpdateURL, "application/json", strings.NewReader(`{"releases":[{"status":"completed","userFraction":1}]}`))
	if err != nil || status != http.StatusBadRequest || !strings.Contains(body, "INVALID_ARGUMENT") {
		t.Errorf("PUT = %d %s %v, want Play's 400", status, body, err)
	}
	if len(tb.errs) != 1 || !strings.Contains(tb.errs[0], "TrackRelease.userFraction at releases[0]") {
		t.Errorf("test failures = %q, want one naming the rule", tb.errs)
	}
	if !strings.Contains(string(w.Transcript()), `"userFraction":1`) {
		t.Error("the refused request must still be in the transcript")
	}
}

func TestWire_AllowInvalid_letsADeliberate400Through(t *testing.T) {
	tb := &recordingTB{TB: t}
	w := testkit.NewWire(tb, testkit.Any(http.StatusTeapot, `{}`)).AllowInvalid("TrackRelease.userFraction")
	status, _, err := send(t, w.Client(), http.MethodPut, tracksUpdateURL, "application/json", strings.NewReader(`{"releases":[{"status":"completed","userFraction":1}]}`))
	if err != nil || status != http.StatusTeapot || len(tb.errs) != 0 {
		t.Errorf("PUT = %d %v, failures %q; want the responder's answer and no failure", status, err, tb.errs)
	}
}

func TestRoundTripFunc_refusesAJSONBodyPlayRefuses(t *testing.T) {
	var seen []byte
	rt := testkit.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = testkit.ReadBody(r)
		return testkit.Response(http.StatusOK, `{}`), nil
	})
	if got := sendRecovering(t, rt, http.MethodPut, tracksUpdateURL, "application/json; charset=UTF-8", `{"releases":[{"status":"completed","userFraction":1}]}`); !strings.Contains(got, "TrackRelease.userFraction") {
		t.Errorf("want a refusal naming TrackRelease.userFraction, got %q", got)
	}
	valid := `{"releases":[{"status":"inProgress","userFraction":0.5}]}`
	if got := sendRecovering(t, rt, http.MethodPut, tracksUpdateURL, "application/json", valid); got != "" || string(seen) != valid {
		t.Errorf("valid body: panic %q, function read %q; want it untouched", got, seen)
	}
	media := []byte("PK\x03\x04 not json")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, bundleUploadURL, io.NopCloser(bytes.NewReader(media)))
	req.Header.Set("Content-Type", "application/octet-stream")
	if _, err := rt.RoundTrip(req); err != nil || !bytes.Equal(seen, media) {
		t.Errorf("media body: err %v, function read %q; want it untouched", err, seen)
	}
}
