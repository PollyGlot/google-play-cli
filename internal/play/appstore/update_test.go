package appstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

// TestUpdateHostedApp_requestShape pins the submit call: POST to the
// app-store-keyed :update endpoint, the whole assembled payload in the body,
// no Edit.
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

	in := appstore.UpdateHostedAppRequest{
		PackageName: "com.example.app",
		AppDetails:  &appstore.AppDetails{DeveloperName: "Acme", ContactEmail: "dev@acme.test"},
		ActiveApks: &appstore.ActiveApks{ActiveApkSets: []appstore.ActiveApkSet{
			{BaseApkID: "apk-base", SplitApkIDs: []string{"apk-en"}},
		}},
		ActiveLocalizedStoreListings: []appstore.StoreListing{
			{LanguageCode: "en-US", AppName: "Acme", FullDescription: "d", AppIconID: "img-icon", ScreenshotIDs: []string{"img-1"}},
		},
		PolicyDeclarations: []appstore.PolicyDeclaration{
			{DeclarationID: "decl-1", Responses: []json.RawMessage{json.RawMessage(`{"questionId":"q1","booleanResponse":{"value":true}}`)}},
		},
	}

	if _, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "com.example.store", in); err != nil {
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

	// Round-trip the body and check every branch of the payload survived,
	// including the un-modelled policy response variant.
	var sent map[string]any
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("body %q is not JSON: %v", gotBody, err)
	}
	for _, key := range []string{"packageName", "appDetails", "activeApks", "activeLocalizedStoreListings", "policyDeclarations"} {
		if _, ok := sent[key]; !ok {
			t.Errorf("body is missing the required %q field: %s", key, gotBody)
		}
	}
	if !strings.Contains(string(gotBody), `"booleanResponse":{"value":true}`) {
		t.Errorf("body dropped the policy response variant: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"screenshotId":["img-1"]`) {
		t.Errorf("body does not use the API's screenshotId spelling: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"splitApkId":["apk-en"]`) {
		t.Errorf("body does not use the API's splitApkId spelling: %s", gotBody)
	}
}

// TestUpdateHostedApp_passesUnknownPolicyVariants proves the oneof stays open:
// a response shape gplay has never heard of reaches Google untouched.
func TestUpdateHostedApp_passesUnknownPolicyVariants(t *testing.T) {
	var gotBody []byte
	rt := testRoundTripper(func(r *http.Request) (*http.Response, error) {
		gotBody, _ = io.ReadAll(r.Body)
		return resp(200, `{}`), nil
	})

	in := appstore.UpdateHostedAppRequest{
		PackageName: "com.example.app",
		PolicyDeclarations: []appstore.PolicyDeclaration{
			{DeclarationID: "d", Responses: []json.RawMessage{json.RawMessage(`{"questionId":"q","futureResponse":{"shape":"unknown"}}`)}},
		},
	}
	if _, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "s", in); err != nil {
		t.Fatalf("UpdateHostedApp: %v", err)
	}
	if !strings.Contains(string(gotBody), `"futureResponse":{"shape":"unknown"}`) {
		t.Errorf("an unmodelled policy response variant was dropped: %s", gotBody)
	}
}

// TestUpdateHostedApp_rawPassthrough keeps ADR-0003 honest even though the
// documented response is empty.
func TestUpdateHostedApp_rawPassthrough(t *testing.T) {
	rt := testRoundTripper(func(*http.Request) (*http.Response, error) {
		return resp(200, `{"unexpected":"field"}`), nil
	})
	raw, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "s", appstore.UpdateHostedAppRequest{PackageName: "p"})
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

// TestUpdatePublishStatus_pathEscaped guards both path keys.
func TestUpdatePublishStatus_pathEscaped(t *testing.T) {
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
			_, err := appstore.UpdateHostedApp(context.Background(), &http.Client{Transport: rt}, "s", appstore.UpdateHostedAppRequest{PackageName: "p"})
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
