package appstorecatalog_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/appstorecatalog"
)

// roundTripperFunc is the offline transport every test in this file rides:
// nothing here ever reaches the network.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const appViewBody = `{
  "appView": {
    "packageName": "com.example.app",
    "appCategory": "APP",
    "appSubcategory": "APPLICATION_TOOLS",
    "activeVersionNames": ["3.1.0", "3.0.9"],
    "lastPublishTime": "2026-06-01T12:00:00Z",
    "firstReleaseDate": {"year": 2019, "month": 4, "day": 7},
    "deliveryToken": "tok-delivery-123",
    "priceInTheUnitedStates": {"currencyCode": "USD", "units": "4", "nanos": 990000000},
    "iarcCertificateId": "iarc-9988",
    "hasInAppPurchases": true,
    "privacyPolicyUrl": "https://example.com/privacy",
    "developerDetails": {"developerName": "Example Ltd"},
    "localizedStoreListings": {
      "defaultLanguageCode": "en-US",
      "localizedStoreListings": [
        {"languageCode": "en-US", "appName": "Example", "shortDescription": "Short one"}
      ]
    },
    "permissions": [{"name": "android.permission.INTERNET"}],
    "deviceCompatibilityRequirements": [{"sdkVersion": {"minSdkVersion": "21", "targetSdkVersion": "34"}}],
    "excludedDevicesByIdentifier": [{"deviceBrand": "acme"}]
  }
}`

// TestGetRecentAppView_requestShape asserts the GET targets the androidpublisher
// appstorecatalog endpoint with BOTH path parameters in place, outside the Edit
// model, and returns the body verbatim for the ADR-0003 pass-through.
func TestGetRecentAppView_requestShape(t *testing.T) {
	var gotURL, gotMethod string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL, gotMethod = r.URL.String(), r.Method
		return resp(200, appViewBody), nil
	})

	v, raw, err := appstorecatalog.GetRecentAppView(context.Background(), &http.Client{Transport: rt}, "com.store.alt", "com.example.app")
	if err != nil {
		t.Fatalf("GetRecentAppView: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	want := "https://androidpublisher.googleapis.com/androidpublisher/v3/appstorecatalog/com.store.alt/recentAppViews/com.example.app"
	if gotURL != want {
		t.Errorf("url = %q, want %q", gotURL, want)
	}
	if strings.Contains(gotURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", gotURL)
	}
	if v.AppView == nil || v.AppView.PackageName != "com.example.app" {
		t.Fatalf("parsed view = %+v, want the catalog app view of com.example.app", v.AppView)
	}
	if v.AppView.DeliveryToken != "tok-delivery-123" {
		t.Errorf("DeliveryToken = %q, want tok-delivery-123", v.AppView.DeliveryToken)
	}
	if len(v.AppView.ActiveVersionNames) != 2 {
		t.Errorf("ActiveVersionNames = %v, want two entries", v.AppView.ActiveVersionNames)
	}
	// ADR-0003: the raw body carries fields the typed view deliberately drops.
	if !strings.Contains(string(raw), "excludedDevicesByIdentifier") {
		t.Errorf("raw = %s, want the verbatim body", raw)
	}
}

// TestGetRecentAppView_escapesPathParams asserts both path parameters are
// escaped, so a value carrying a slash or a space cannot forge a different path.
func TestGetRecentAppView_escapesPathParams(t *testing.T) {
	var gotPath string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.EscapedPath()
		return resp(200, `{}`), nil
	})

	if _, _, err := appstorecatalog.GetRecentAppView(context.Background(), &http.Client{Transport: rt}, "com.store/../evil", "com.example app"); err != nil {
		t.Fatalf("GetRecentAppView: %v", err)
	}
	if strings.Contains(gotPath, "com.store/../evil") {
		t.Errorf("path %q must escape the app store package name", gotPath)
	}
	if !strings.Contains(gotPath, "com.store%2F..%2Fevil") {
		t.Errorf("path %q should carry the escaped app store package name", gotPath)
	}
	if !strings.Contains(gotPath, "com.example%20app") {
		t.Errorf("path %q should carry the escaped Play app package name", gotPath)
	}
}

// TestGetRecentAppView_apiError maps a non-2xx to an *api.Error carrying the
// status (so the shared classifier can map 403 → exit 11) and the RPC id.
func TestGetRecentAppView_apiError(t *testing.T) {
	rt := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return resp(403, `{"error":{"message":"The caller does not have permission"}}`), nil
	})

	_, _, err := appstorecatalog.GetRecentAppView(context.Background(), &http.Client{Transport: rt}, "com.store.alt", "com.example.app")
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error", err)
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
	if apiErr.Operation != "appstorecatalog.recentappviews.get" {
		t.Errorf("Operation = %q, want the recentappviews.get RPC id", apiErr.Operation)
	}
}
