package view_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	viewcmd "github.com/PollyGlot/google-play-cli/commands/appstore/catalog/view"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// catalogRT answers the /token exchange and the recentappviews.get call,
// recording the API URL. Nothing here reaches the network.
type catalogRT struct {
	mu     sync.Mutex
	calls  []string
	apiURL string
	status int
	body   string
}

func (r *catalogRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.apiURL = req.URL.String()
	if r.status != 0 {
		return jsonResp(r.status, r.body), nil
	}
	return jsonResp(200, appViewBody), nil
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
        {"languageCode": "en-US", "appName": "Example", "shortDescription": "Short one"},
        {"languageCode": "fr-FR", "appName": "Exemplaire", "shortDescription": "Court"}
      ]
    },
    "permissions": [{"name": "android.permission.INTERNET"}, {"name": "android.permission.CAMERA", "maxSdkVersion": 28}],
    "deviceCompatibilityRequirements": [
      {"sdkVersion": {"minSdkVersion": "21", "maxSdkVersion": "34", "targetSdkVersion": "33"}, "nativePlatforms": ["arm64-v8a"]}
    ],
    "excludedDevicesByIdentifier": [{"deviceBrand": "acme"}]
  }
}`

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// TestRun_happyPath_storeAxis_noEdit asserts the read hits the appstorecatalog
// endpoint with both path parameters, opens no Edit, and passes the
// RecentAppView through verbatim on --output json (ADR-0003).
func TestRun_happyPath_storeAxis_noEdit(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &catalogRT{}
	rc := newRC(t, rt)

	r, err := viewcmd.Run(rc, viewcmd.Input{StorePackage: "com.store.alt", PlayPackage: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasSuffix(rt.apiURL, "/appstorecatalog/com.store.alt/recentAppViews/com.example.app") {
		t.Errorf("url %q is not the recentappviews.get endpoint", rt.apiURL)
	}
	if strings.Contains(rt.apiURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", rt.apiURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"excludedDevicesByIdentifier"`) {
		t.Errorf("json %s should pass the RecentAppView through verbatim", out.String())
	}
}

// TestRun_humanSummary renders the table view and asserts the main catalog
// fields land: identity, versions, price, delivery token, IARC, listings,
// permissions and device compatibility.
func TestRun_humanSummary(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &catalogRT{}
	rc := newRC(t, rt)

	r, err := viewcmd.Run(rc, viewcmd.Input{StorePackage: "com.store.alt", PlayPackage: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"com.example.app",
		"APP / APPLICATION_TOOLS",
		"Example Ltd",
		"3.1.0, 3.0.9",
		"2026-06-01T12:00:00Z",
		"2019-04-07",
		"4.99 USD",
		"iarc-9988",
		"tok-delivery-123",
		"en-US",
		"fr-FR  Exemplaire — Court",
		"android.permission.CAMERA (maxSdkVersion 28)",
		"sdk 21..34 (target 33) — abis: arm64-v8a",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("table %q missing %q", got, want)
		}
	}
}

// TestRun_markdownRecord asserts the markdown view renders a heading plus the
// field list, so a pasted report stands alone.
func TestRun_markdownRecord(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &catalogRT{}
	rc := newRC(t, rt)

	r, err := viewcmd.Run(rc, viewcmd.Input{StorePackage: "com.store.alt", PlayPackage: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().Markdown(&out); err != nil {
		t.Fatalf("Markdown: %v", err)
	}
	got := out.String()
	for _, want := range []string{"## Catalog app view — com.example.app", "- **Delivery token**: tok-delivery-123", "**Permissions**"} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown %q missing %q", got, want)
		}
	}
}

// TestRun_freeApp_rendersFree asserts an app with no US price reads as "free"
// rather than an empty cell (the API expresses free by omitting the Money).
func TestRun_freeApp_rendersFree(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &catalogRT{status: 200, body: `{"appView":{"packageName":"com.example.free"}}`}
	rc := newRC(t, rt)

	r, err := viewcmd.Run(rc, viewcmd.Input{StorePackage: "com.store.alt", PlayPackage: "com.example.free"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(out.String(), "PRICE\tfree") {
		t.Errorf("table %q should render a priceless app as free", out.String())
	}
}

// TestRun_storePackageFromEnv asserts the app store package name resolves from
// $GPLAY_APP_STORE_PACKAGE when --store-package is omitted — the CI path.
func TestRun_storePackageFromEnv(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "com.store.fromenv")
	rt := &catalogRT{}
	rc := newRC(t, rt)

	if _, err := viewcmd.Run(rc, viewcmd.Input{PlayPackage: "com.example.app"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.apiURL, "/appstorecatalog/com.store.fromenv/") {
		t.Errorf("url %q should use the app store package name from the environment", rt.apiURL)
	}
}

// TestRun_flagBeatsEnv asserts --store-package wins over the env var (later
// layer wins, ADR-0004).
func TestRun_flagBeatsEnv(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "com.store.fromenv")
	rt := &catalogRT{}
	rc := newRC(t, rt)

	if _, err := viewcmd.Run(rc, viewcmd.Input{StorePackage: "com.store.fromflag", PlayPackage: "com.example.app"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.apiURL, "/appstorecatalog/com.store.fromflag/") {
		t.Errorf("url %q should prefer --store-package over the environment", rt.apiURL)
	}
}

// TestRun_missingStorePackage_exit2_noNetwork asserts an unresolved app store
// package name is CLI misuse caught before any HTTP call, naming both layers.
func TestRun_missingStorePackage_exit2_noNetwork(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &catalogRT{}
	rc := newRC(t, rt)

	_, err := viewcmd.Run(rc, viewcmd.Input{PlayPackage: "com.example.app"})
	assertExit(t, err, 2)
	for _, want := range []string{"--store-package", appstorecmd.EnvStorePackage} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("usage error %q must name %q", err.Error(), want)
		}
	}
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_missingPlayPackage_exit2_noNetwork asserts a whitespace-only Play app
// package name is CLI misuse caught before any HTTP call.
func TestRun_missingPlayPackage_exit2_noNetwork(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "com.store.alt")
	rt := &catalogRT{}
	rc := newRC(t, rt)

	_, err := viewcmd.Run(rc, viewcmd.Input{PlayPackage: "   "})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_403_namesStorePackage asserts a forbidden read maps to exit 11 and
// the refusal points at the app store enrollment, not a per-app permission.
func TestRun_403_namesStorePackage(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &catalogRT{status: 403, body: `{"error":{"message":"The caller does not have permission"}}`}
	rc := newRC(t, rt)

	_, err := viewcmd.Run(rc, viewcmd.Input{StorePackage: "com.store.alt", PlayPackage: "com.example.app"})
	assertExit(t, err, 11)
	if !strings.Contains(err.Error(), "com.store.alt") {
		t.Errorf("403 refusal %q must name the app store package", err.Error())
	}
}

// TestRun_404_exit30 asserts an app with no catalog app view maps to the
// not-found exit code with an eligibility hint.
func TestRun_404_exit30(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &catalogRT{status: 404, body: `{"error":{"message":"not found"}}`}
	rc := newRC(t, rt)

	_, err := viewcmd.Run(rc, viewcmd.Input{StorePackage: "com.store.alt", PlayPackage: "com.example.gone"})
	assertExit(t, err, 30)
	if !strings.Contains(err.Error(), "eligible") {
		t.Errorf("404 hint %q should explain catalog eligibility", err.Error())
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v has no ExitCode", err)
	}
	if got := c.ExitCode(); got != want {
		t.Errorf("exit = %d, want %d", got, want)
	}
}
