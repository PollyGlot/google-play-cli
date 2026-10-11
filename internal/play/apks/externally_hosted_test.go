package apks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/apks"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestAddExternallyHosted_postsResourceVerbatim_returnsVersionCode asserts the
// wire contract of edits.apks.addexternallyhosted: a JSON POST on the Edit's
// /apks/externallyHosted path whose envelope carries the caller's resource
// byte for byte, and the versionCode read back from Google's answer.
func TestAddExternallyHosted_postsResourceVerbatim_returnsVersionCode(t *testing.T) {
	f := testkit.NewFake(testkit.Any(http.StatusOK, `{"externallyHostedApk":{"packageName":"com.example.app","versionCode":310}}`))
	apk := json.RawMessage(`{"packageName":"com.example.app","externallyHostedUrl":"https://cdn.example.com/app.apk","versionCode":310}`)

	code, raw, err := apks.AddExternallyHosted(context.Background(), &http.Client{Transport: f}, "com.example.app", "edit-1", apk)
	if err != nil {
		t.Fatalf("AddExternallyHosted: %v", err)
	}
	if code != 310 {
		t.Errorf("versionCode = %d, want 310", code)
	}
	if len(raw) == 0 {
		t.Error("raw response is empty, want the verbatim body")
	}
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.Method != http.MethodPost || c.Path != "/androidpublisher/v3/applications/com.example.app/edits/edit-1/apks/externallyHosted" {
		t.Errorf("request = %s %s, want POST .../edits/edit-1/apks/externallyHosted", c.Method, c.Path)
	}
	var body struct {
		ExternallyHostedApk json.RawMessage `json:"externallyHostedApk"`
	}
	if err := json.Unmarshal(c.Body, &body); err != nil {
		t.Fatalf("request body %s: %v", c.Body, err)
	}
	if string(body.ExternallyHostedApk) != string(apk) {
		t.Errorf("externallyHostedApk = %s, want the resource verbatim %s", body.ExternallyHostedApk, apk)
	}
}

// TestAddExternallyHosted_refusalPassesThrough asserts Google's refusal (the
// method is Managed Play only) surfaces as the API error with its own exit
// code, never a client-side guess.
func TestAddExternallyHosted_refusalPassesThrough(t *testing.T) {
	f := testkit.NewFake(testkit.Any(http.StatusForbidden, `{"error":{"code":403,"message":"externally hosted APKs are restricted","status":"PERMISSION_DENIED"}}`))

	_, _, err := apks.AddExternallyHosted(context.Background(), &http.Client{Transport: f}, "com.example.app", "edit-1", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("AddExternallyHosted succeeded on a 403, want an error")
	}
	if got := exit.For(err); got == 0 || got == 20 {
		t.Errorf("exit = %d, want the API taxonomy code for a 403", got)
	}
}
