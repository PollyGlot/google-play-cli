package appstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

// testRoundTripper is the offline transport every test in this package runs
// through: no test here ever reaches the network.
type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestCreateHostedApp_requestShape pins the wire shape of the create call: POST
// to the app-store-keyed :create endpoint, the app package in the JSON body
// (never in the path), a JSON content type, and no Edit.
func TestCreateHostedApp_requestShape(t *testing.T) {
	var gotMethod, gotURL, gotCT string
	var gotBody []byte
	rt := testRoundTripper(func(r *http.Request) (*http.Response, error) {
		gotMethod = r.Method
		gotURL = r.URL.String()
		gotCT = r.Header.Get("Content-Type")
		if r.Body != nil {
			gotBody, _ = io.ReadAll(r.Body)
		}
		return resp(200, `{}`), nil
	})

	raw, err := appstore.CreateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app")
	if err != nil {
		t.Fatalf("CreateHostedApp: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotURL, "/appstore/com.example.store/apps:create") {
		t.Errorf("url %q is not the appstoreappsreview.createAppStoreHostedApp endpoint", gotURL)
	}
	if strings.Contains(gotURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", gotURL)
	}
	// The app package rides the BODY, not the path: the path key is the app
	// store package name. A path carrying the app package would be a different
	// (later-slice) endpoint.
	if strings.Contains(gotURL, "com.example.app") {
		t.Errorf("url %q must not carry the app package: it belongs in the request body", gotURL)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	var sent appstore.CreateHostedAppRequest
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("request body %q is not JSON: %v", gotBody, err)
	}
	if sent.PackageName != "com.example.app" {
		t.Errorf("body packageName = %q, want com.example.app", sent.PackageName)
	}
	if string(raw) != `{}` {
		t.Errorf("raw = %q, want the verbatim body", raw)
	}
}

// TestCreateHostedApp_storePackagePathEscaped asserts the app store package name
// is path-escaped, so a stray reserved character cannot break out of its path
// segment.
func TestCreateHostedApp_storePackagePathEscaped(t *testing.T) {
	var gotURL string
	rt := testRoundTripper(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return resp(200, `{}`), nil
	})
	if _, err := appstore.CreateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example store/x", "com.example.app"); err != nil {
		t.Fatalf("CreateHostedApp: %v", err)
	}
	if strings.Contains(gotURL, "com.example store") {
		t.Errorf("url %q did not escape the app store package name", gotURL)
	}
	if !strings.Contains(gotURL, "com.example%20store%2Fx") {
		t.Errorf("url %q missing the escaped app store package name", gotURL)
	}
	// The escaping must not swallow the :create suffix.
	if !strings.HasSuffix(gotURL, "/apps:create") {
		t.Errorf("url %q lost the :create suffix", gotURL)
	}
}

// TestCreateHostedApp_rawPassthrough asserts the verbatim body is returned even
// when it carries fields the (currently empty) response schema does not model:
// ADR-0003 pass-through must never be filtered through a typed struct.
func TestCreateHostedApp_rawPassthrough(t *testing.T) {
	const body = `{"unmodeledFutureField":"kept","nested":{"a":1}}`
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		return resp(200, body), nil
	})
	raw, err := appstore.CreateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app")
	if err != nil {
		t.Fatalf("CreateHostedApp: %v", err)
	}
	if string(raw) != body {
		t.Errorf("raw = %q, want verbatim %q", raw, body)
	}
}

// TestCreateHostedApp_emptyBody asserts a 2xx with no body is not an error: the
// response schema carries no fields, so an acknowledgement is a success.
func TestCreateHostedApp_emptyBody(t *testing.T) {
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		return resp(200, ""), nil
	})
	raw, err := appstore.CreateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app")
	if err != nil {
		t.Fatalf("CreateHostedApp: %v", err)
	}
	if len(raw) != 0 {
		t.Errorf("raw = %q, want empty", raw)
	}
}

// TestCreateHostedApp_403_exit11 asserts a forbidden create (the caller is not
// an enrolled app store, or the service account lacks the grant) maps to the
// authz exit code.
func TestCreateHostedApp_403_exit11(t *testing.T) {
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		return resp(403, `{"error":{"message":"The caller does not have permission"}}`), nil
	})
	_, err := appstore.CreateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app")
	assertExit(t, err, 11)
}

// TestCreateHostedApp_404_exit30 asserts an unknown app store package maps to
// the API-misuse exit code.
func TestCreateHostedApp_404_exit30(t *testing.T) {
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		return resp(404, `{"error":{"message":"app store not found"}}`), nil
	})
	_, err := appstore.CreateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.unknown.store", "com.example.app")
	assertExit(t, err, 30)
}

// TestCreateHostedApp_409_exit60 asserts an already-created hosted app record
// (the natural repeat-call rejection) maps to the state-conflict exit code.
func TestCreateHostedApp_409_exit60(t *testing.T) {
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		return resp(409, `{"error":{"message":"already exists"}}`), nil
	})
	_, err := appstore.CreateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app")
	assertExit(t, err, 60)
}

// TestCreateHostedApp_5xx_exit40 asserts an upstream outage maps to the
// retry-safe exit code.
func TestCreateHostedApp_5xx_exit40(t *testing.T) {
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		return resp(503, `{"error":{"message":"backend unavailable"}}`), nil
	})
	_, err := appstore.CreateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app")
	assertExit(t, err, 40)
}

// TestCreateHostedApp_transport_exit50 asserts a transport failure (no HTTP
// response at all) maps to the network exit code.
func TestCreateHostedApp_transport_exit50(t *testing.T) {
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	_, err := appstore.CreateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app")
	assertExit(t, err, 50)
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err %v is not *api.Error", err)
	}
	if got := apiErr.ExitCode(); got != want {
		t.Errorf("exit = %d, want %d", got, want)
	}
}
