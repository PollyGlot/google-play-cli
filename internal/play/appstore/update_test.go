package appstore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

// submissionBody is a complete submission as an operator would write it,
// including a policy response variant appstore.PolicyDeclaration models only as
// json.RawMessage.
const submissionBody = `{
  "appDetails": {"developerName": "Acme", "contactEmail": "dev@acme.test"},
  "activeApks": {"activeApkSets": [{"baseApkId": "apk-base", "splitApkId": ["apk-en"]}]},
  "activeLocalizedStoreListings": [
    {"languageCode": "en-US", "appName": "Acme", "fullDescription": "d", "appIconId": "img-icon", "screenshotId": ["img-1"]}
  ],
  "policyDeclarations": [
    {"declarationId": "decl-1", "responses": [{"questionId": "q1", "documentResponse": {"documentId": "file-9", "nonExpiring": true}}]}
  ]
}`

// TestUpdateHostedApp_requestShape pins the submit call: POST to the
// app-store-keyed :update endpoint, no Edit, and the whole payload on the wire.
func TestUpdateHostedApp_requestShape(t *testing.T) {
	var gotMethod, gotURL string
	var gotBody []byte
	rt := testRoundTripper(func(r *http.Request) (*http.Response, error) {
		gotMethod, gotURL = r.Method, r.URL.String()
		if r.Body != nil {
			gotBody, _ = io.ReadAll(r.Body)
		}
		return resp(200, `{}`), nil
	})

	if _, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app", json.RawMessage(submissionBody)); err != nil {
		t.Fatalf("UpdateHostedApp: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotURL, "/appstore/com.example.store/apps:update") {
		t.Errorf("url %q is not the updateAppStoreHostedApp endpoint", gotURL)
	}
	if strings.Contains(gotURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", gotURL)
	}

	var sent map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("request body %q is not JSON: %v", gotBody, err)
	}
	for _, key := range []string{"packageName", "appDetails", "activeApks", "activeLocalizedStoreListings", "policyDeclarations"} {
		if _, ok := sent[key]; !ok {
			t.Errorf("request body is missing the %q field: %s", key, gotBody)
		}
	}
	// The media ids from the upload verbs must survive under the API's own
	// spelling — they are the whole point of having uploaded anything.
	got := compact(t, gotBody)
	for _, want := range []string{`"baseApkId":"apk-base"`, `"splitApkId":["apk-en"]`, `"appIconId":"img-icon"`, `"screenshotId":["img-1"]`, `"documentId":"file-9"`} {
		if !strings.Contains(got, want) {
			t.Errorf("request body is missing %s: %s", want, got)
		}
	}
}

// TestUpdateHostedApp_forcesPackageName: the caller's resolved target wins over
// whatever packageName the body carried, so the command layer's reconciliation
// cannot be bypassed by the file.
func TestUpdateHostedApp_forcesPackageName(t *testing.T) {
	for _, body := range []string{`{"packageName":"com.stale.app"}`, `{}`} {
		var gotBody []byte
		rt := testRoundTripper(func(r *http.Request) (*http.Response, error) {
			gotBody, _ = io.ReadAll(r.Body)
			return resp(200, `{}`), nil
		})
		if _, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "s", "com.example.app", json.RawMessage(body)); err != nil {
			t.Fatalf("UpdateHostedApp(%s): %v", body, err)
		}
		var sent struct {
			PackageName string `json:"packageName"`
		}
		if err := json.Unmarshal(gotBody, &sent); err != nil {
			t.Fatalf("request body %q: %v", gotBody, err)
		}
		if sent.PackageName != "com.example.app" {
			t.Errorf("packageName = %q for body %s, want the resolved target", sent.PackageName, body)
		}
	}
}

// TestUpdateHostedApp_forwardsUnmodelledFields is the reason the body is
// forwarded verbatim instead of round-tripped through UpdateHostedAppRequest.
// Anything the struct does not model — a field Google added, or an operator's
// typo — must reach Google as written: dropping it would ship an incomplete
// submission that cannot be recalled, and a typo must come back as a readable
// rejection rather than as silence.
func TestUpdateHostedApp_forwardsUnmodelledFields(t *testing.T) {
	body := `{
      "appDetails": {"developerName": "Acme", "developerNAme": "typo", "futureField": {"x": 1}},
      "brandNewTopLevelKey": ["a", "b"],
      "policyDeclarations": [{"declarationId": "d", "responses": [{"questionId": "q", "futureResponse": {"shape": "unknown"}}]}]
    }`
	var gotBody []byte
	rt := testRoundTripper(func(r *http.Request) (*http.Response, error) {
		gotBody, _ = io.ReadAll(r.Body)
		return resp(200, `{}`), nil
	})

	if _, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "s", "p", json.RawMessage(body)); err != nil {
		t.Fatalf("UpdateHostedApp: %v", err)
	}
	got := compact(t, gotBody)
	for _, want := range []string{`"developerNAme":"typo"`, `"futureField":{"x":1}`, `"brandNewTopLevelKey":["a","b"]`, `"futureResponse":{"shape":"unknown"}`} {
		if !strings.Contains(got, want) {
			t.Errorf("the wire body dropped %s: %s", want, got)
		}
	}
}

// TestUpdateHostedApp_invalidBody_errors: a body that is not a JSON object
// cannot have packageName forced onto it, and must fail before the request.
func TestUpdateHostedApp_invalidBody_errors(t *testing.T) {
	var called bool
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		called = true
		return resp(200, `{}`), nil
	})
	if _, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "s", "p", json.RawMessage(`["not","an","object"]`)); err == nil {
		t.Fatal("want an error for a non-object body, got nil")
	}
	if called {
		t.Error("a malformed body must not reach the API")
	}
}

// TestUpdateHostedApp_rawPassthrough keeps ADR-0003 honest even though the
// documented response is empty.
func TestUpdateHostedApp_rawPassthrough(t *testing.T) {
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		return resp(200, `{"unexpected":"field"}`), nil
	})
	raw, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "s", "p", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("UpdateHostedApp: %v", err)
	}
	if string(raw) != `{"unexpected":"field"}` {
		t.Errorf("raw = %s, want the verbatim response", raw)
	}
}

// TestUpdatePublishStatus_requestShape pins the publish-state call: the hosted
// package is a path segment here (unlike :create, where it rides the body),
// and the state travels as the API enum.
func TestUpdatePublishStatus_requestShape(t *testing.T) {
	var gotURL string
	var gotBody []byte
	rt := testRoundTripper(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotBody, _ = io.ReadAll(r.Body)
		return resp(200, `{}`), nil
	})

	if _, err := appstore.UpdatePublishStatus(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app", appstore.PublishStateUnpublished); err != nil {
		t.Fatalf("UpdatePublishStatus: %v", err)
	}
	if !strings.HasSuffix(gotURL, "/appstore/com.example.store/apps/com.example.app:updateAppStoreHostedAppPublishStatus") {
		t.Errorf("url %q is not the updateAppStoreHostedAppPublishStatus endpoint", gotURL)
	}
	var body struct {
		PublishState string `json:"publishState"`
	}
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", gotBody, err)
	}
	if body.PublishState != "APP_STORE_APP_PUBLISH_STATE_UNPUBLISHED" {
		t.Errorf("publishState = %q, want the API enum", body.PublishState)
	}
}

// TestUpdate_pathEscaped guards both path keys, on both verbs.
func TestUpdate_pathEscaped(t *testing.T) {
	var gotURL string
	rt := testRoundTripper(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return resp(200, `{}`), nil
	})
	if _, err := appstore.UpdatePublishStatus(context.Background(), &http.Client{Transport: rt}, "com.example store", "com.example/app", appstore.PublishStatePublished); err != nil {
		t.Fatalf("UpdatePublishStatus: %v", err)
	}
	if !strings.Contains(gotURL, "/appstore/com.example%20store/apps/com.example%2Fapp:") {
		t.Errorf("url %q does not escape both path keys", gotURL)
	}

	if _, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example store", "p", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("UpdateHostedApp: %v", err)
	}
	if !strings.Contains(gotURL, "/appstore/com.example%20store/apps:update") {
		t.Errorf("url %q does not escape the store package", gotURL)
	}
}

// TestUpdate_errorTaxonomy pins that both verbs keep the shared exit codes.
func TestUpdate_errorTaxonomy(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   int
	}{
		{"forbidden", http.StatusForbidden, 11},
		{"not found", http.StatusNotFound, 30},
		{"conflict", http.StatusConflict, 60},
		{"server error", http.StatusInternalServerError, 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
				return resp(tc.status, `{"error":{"message":"boom"}}`), nil
			})
			_, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "s", "p", json.RawMessage(`{}`))
			assertExitCode(t, err, tc.want)

			_, err = appstore.UpdatePublishStatus(context.Background(), &http.Client{Transport: rt}, "s", "p", appstore.PublishStatePublished)
			assertExitCode(t, err, tc.want)
		})
	}
}

// TestUpdate_transport_exit50 keeps a dial failure distinct from an API refusal.
func TestUpdate_transport_exit50(t *testing.T) {
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	_, err := appstore.UpdatePublishStatus(context.Background(), &http.Client{Transport: rt}, "s", "p", appstore.PublishStatePublished)
	assertExitCode(t, err, 50)
}

// compact normalises whitespace so a body assertion tests content, not layout.
func compact(t *testing.T, body []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, body); err != nil {
		t.Fatalf("compact %q: %v", body, err)
	}
	return buf.String()
}
