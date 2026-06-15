// Package detailscmd_test also exercises `gplay apps details set` (the
// field-by-field write) at the kernel level. The key assertions are on
// the details.patch BODY actually emitted: a partial patch must carry
// ONLY the fields the user passed — an omitted flag is absent from the
// body (the field stays intact upstream), and an explicit empty value is
// sent verbatim (it clears the field). The write runs inside an implicit
// Edit (open → details.patch → commit); --dry-run previews the patch with
// zero network and zero auth.
package detailscmd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/detailscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// setRT terminates the OAuth2 /token exchange and routes every
// androidpublisher call needed by an App details write: edits.insert,
// details.patch (PATCH, body captured), edits.commit, and edits.delete
// (the failure/discard path). Configurable status codes on the patch and
// commit exercise the error and discard paths.
type setRT struct {
	t          *testing.T
	editID     string
	patchResp  string
	patchCode  int // 0 → 200
	commitCode int // 0 → 200

	mu        sync.Mutex
	calls     []string
	tokenHits int
	patchReq  []byte
}

func (r *setRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}

	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodPatch && strings.HasSuffix(req.URL.Path, "/details"):
		body, _ := io.ReadAll(req.Body)
		r.patchReq = body
		code := r.patchCode
		if code == 0 {
			code = 200
		}
		resp := r.patchResp
		if resp == "" {
			resp = string(body) // echo the patch as the resulting resource
		}
		return jsonResp(code, resp), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		code := r.commitCode
		if code == 0 {
			code = 200
		}
		return jsonResp(code, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, r.editID)), nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

// TestSet_onlyChangedField_inBody asserts the core partial-patch
// contract: `set --contact-email X` emits a details.patch body carrying
// ONLY contactEmail — defaultLanguage, contactPhone, and contactWebsite
// are absent (they stay intact upstream). The full implicit-Edit sequence
// runs (/token → insert → PATCH → commit) and --output json is the
// details.patch response verbatim.
func TestSet_onlyChangedField_inBody(t *testing.T) {
	rt := &setRT{t: t, editID: "edit-set"}
	rc, _ := newRC(t, rt)

	r, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:         "com.example.app",
		ContactEmail:    "new@example.com",
		ContactEmailSet: true,
	})
	if err != nil {
		t.Fatalf("RunSet: %v", err)
	}
	if r == nil {
		t.Fatal("RunSet returned nil Renderable")
	}

	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"PATCH /androidpublisher/v3/applications/com.example.app/edits/edit-set/details",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-set:commit",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	// The patch body carries ONLY contactEmail.
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rt.patchReq, &body); err != nil {
		t.Fatalf("patch body is not JSON: %v\nbody=%s", err, rt.patchReq)
	}
	if _, ok := body["contactEmail"]; !ok {
		t.Errorf("patch body missing contactEmail: %s", rt.patchReq)
	}
	for _, absent := range []string{"defaultLanguage", "contactPhone", "contactWebsite"} {
		if _, ok := body[absent]; ok {
			t.Errorf("patch body unexpectedly contains %q (a set of one field must not touch the others): %s", absent, rt.patchReq)
		}
	}

	// --output json is the details.patch response verbatim.
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(jsonOut.String(), "new@example.com") {
		t.Errorf("JSON output = %s, want the details.patch response verbatim", jsonOut.String())
	}
}

// TestSet_clearField_sendsEmpty asserts that an explicit empty value
// (`--contact-phone ""`) is sent verbatim (clears the field), while a
// field NOT passed is absent from the body.
func TestSet_clearField_sendsEmpty(t *testing.T) {
	rt := &setRT{t: t, editID: "edit-clear"}
	rc, _ := newRC(t, rt)

	_, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:         "com.example.app",
		ContactPhone:    "",
		ContactPhoneSet: true,
	})
	if err != nil {
		t.Fatalf("RunSet: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rt.patchReq, &body); err != nil {
		t.Fatalf("patch body is not JSON: %v\nbody=%s", err, rt.patchReq)
	}
	raw, ok := body["contactPhone"]
	if !ok {
		t.Fatalf("patch body missing contactPhone (an explicit empty value must be sent to clear): %s", rt.patchReq)
	}
	if string(raw) != `""` {
		t.Errorf("contactPhone = %s, want \"\" (empty string, to clear the field)", raw)
	}
	for _, absent := range []string{"defaultLanguage", "contactEmail", "contactWebsite"} {
		if _, ok := body[absent]; ok {
			t.Errorf("patch body unexpectedly contains %q: %s", absent, rt.patchReq)
		}
	}
}

// TestSet_multipleFields_inBody asserts that passing several flags emits
// all of them (and only them) — here defaultLanguage + contactWebsite.
func TestSet_multipleFields_inBody(t *testing.T) {
	rt := &setRT{t: t, editID: "edit-multi"}
	rc, _ := newRC(t, rt)

	_, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:            "com.example.app",
		DefaultLanguage:    "fr-FR",
		DefaultLanguageSet: true,
		ContactWebsite:     "https://support.example.fr",
		ContactWebsiteSet:  true,
	})
	if err != nil {
		t.Fatalf("RunSet: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rt.patchReq, &body); err != nil {
		t.Fatalf("patch body is not JSON: %v\nbody=%s", err, rt.patchReq)
	}
	for _, present := range []string{"defaultLanguage", "contactWebsite"} {
		if _, ok := body[present]; !ok {
			t.Errorf("patch body missing %q: %s", present, rt.patchReq)
		}
	}
	for _, absent := range []string{"contactEmail", "contactPhone"} {
		if _, ok := body[absent]; ok {
			t.Errorf("patch body unexpectedly contains %q: %s", absent, rt.patchReq)
		}
	}
}

// TestSet_noFieldFlag_exit2_noHTTP asserts the no-flag guard: a bare
// `set` with no field flag is misuse — it short-circuits with exit 2
// before any HTTP, so a forgotten flag can never emit an empty patch.
func TestSet_noFieldFlag_exit2_noHTTP(t *testing.T) {
	rt := &setRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := detailscmd.RunSet(rc, detailscmd.SetInput{Package: "com.example.app"})
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestSet_dryRun_noHTTP asserts --dry-run previews the patch with zero
// HTTP (and zero auth) and still returns a non-nil Renderable whose table
// view shows the field that would be written.
func TestSet_dryRun_noHTTP(t *testing.T) {
	rt := &setRT{t: t}
	rc, _ := newRC(t, rt)

	r, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:         "com.example.app",
		ContactEmail:    "new@example.com",
		ContactEmailSet: true,
		DryRun:          true,
	})
	if err != nil {
		t.Fatalf("RunSet: %v", err)
	}
	if r == nil {
		t.Fatal("RunSet returned nil Renderable on --dry-run")
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls on --dry-run, saw: %v", rt.calls)
	}

	var tableOut bytes.Buffer
	if err := r.Renderers().Table(&tableOut); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := tableOut.String()
	if !strings.Contains(out, "new@example.com") {
		t.Errorf("dry-run preview = %q, want the field that would be written", out)
	}
	if !strings.Contains(strings.ToLower(out), "dry-run") {
		t.Errorf("dry-run preview = %q, want a dry-run marker", out)
	}
}

// TestSet_dryRunJSON_previewsPatch asserts that --dry-run under --output
// json (the DEFAULT in pipes/CI) produces a parseable preview object —
// tagged dryRun:true and carrying only the touched fields — rather than
// erroring. This is the contract a CI pipeline relies on; it must not
// regress. Still zero HTTP.
func TestSet_dryRunJSON_previewsPatch(t *testing.T) {
	rt := &setRT{t: t}
	rc, _ := newRC(t, rt)

	r, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:         "com.example.app",
		ContactEmail:    "new@example.com",
		ContactEmailSet: true,
		DryRun:          true,
	})
	if err != nil {
		t.Fatalf("RunSet: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls on --dry-run, saw: %v", rt.calls)
	}

	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render must not error on --dry-run (json is the CI default): %v", err)
	}
	var preview struct {
		DryRun bool `json:"dryRun"`
		Patch  struct {
			ContactEmail *string `json:"contactEmail"`
			ContactPhone *string `json:"contactPhone"`
		} `json:"patch"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &preview); err != nil {
		t.Fatalf("dry-run JSON is not a parseable preview object: %v\nout=%s", err, jsonOut.String())
	}
	if !preview.DryRun {
		t.Errorf("preview.dryRun = false, want true; out=%s", jsonOut.String())
	}
	if preview.Patch.ContactEmail == nil || *preview.Patch.ContactEmail != "new@example.com" {
		t.Errorf("preview.patch.contactEmail = %v, want \"new@example.com\"", preview.Patch.ContactEmail)
	}
	if preview.Patch.ContactPhone != nil {
		t.Errorf("preview.patch.contactPhone = %v, want absent (untouched field)", *preview.Patch.ContactPhone)
	}
}

// TestSet_dryRun_worksWithNoAccount asserts --dry-run never touches auth:
// even with no resolved Account it previews successfully.
func TestSet_dryRun_worksWithNoAccount(t *testing.T) {
	rt := &setRT{t: t}
	rc, _ := newRC(t, rt)
	rc.Account = nil

	_, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:         "com.example.app",
		ContactEmail:    "new@example.com",
		ContactEmailSet: true,
		DryRun:          true,
	})
	if err != nil {
		t.Fatalf("RunSet with --dry-run and no Account should succeed offline, got: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls on --dry-run, saw: %v", rt.calls)
	}
}

// TestSet_commitFailure_discardsEdit asserts that a failure on commit
// auto-discards the Edit (a DELETE is seen) so a dangling Edit does not
// block the next publish, and the 5xx maps to exit 40.
func TestSet_commitFailure_discardsEdit(t *testing.T) {
	rt := &setRT{t: t, editID: "edit-commitfail", commitCode: 500}
	rc, _ := newRC(t, rt)

	_, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:         "com.example.app",
		ContactEmail:    "new@example.com",
		ContactEmailSet: true,
	})
	if got := exit.For(err); got != 40 {
		t.Errorf("exit.For(err) = %d, want 40 (5xx); err=%v", got, err)
	}
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-commitfail") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after commit failure; calls = %v", rt.calls)
	}
}

// TestSet_keepEditOnFailure_danglingError asserts that with
// --keep-edit-on-failure a failed write is wrapped in a
// *edits.DanglingEditError carrying the Edit ID, and the Edit is NOT
// discarded (no DELETE).
func TestSet_keepEditOnFailure_danglingError(t *testing.T) {
	rt := &setRT{
		t:         t,
		editID:    "edit-keep",
		patchCode: 500,
		patchResp: `{"error":{"code":500,"message":"backend error"}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:           "com.example.app",
		ContactEmail:      "new@example.com",
		ContactEmailSet:   true,
		KeepEditOnFailure: true,
	})
	var dangling *edits.DanglingEditError
	if !errors.As(err, &dangling) {
		t.Fatalf("err = %v (%T), want *edits.DanglingEditError", err, err)
	}
	if dangling.EditID != "edit-keep" {
		t.Errorf("DanglingEditError.EditID = %q, want %q", dangling.EditID, "edit-keep")
	}
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") {
			t.Errorf("Edit was discarded despite --keep-edit-on-failure; calls = %v", rt.calls)
		}
	}
}

// TestSet_patch403_exit11_apiAccessHint asserts a 403 on details.patch
// maps to exit 11 with the Play Console API access hint.
func TestSet_patch403_exit11_apiAccessHint(t *testing.T) {
	rt := &setRT{
		t:         t,
		editID:    "edit-403",
		patchCode: 403,
		patchResp: `{"error":{"code":403,"message":"insufficient permissions"}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:         "com.example.app",
		ContactEmail:    "new@example.com",
		ContactEmailSet: true,
	})
	if got := exit.For(err); got != 11 {
		t.Errorf("exit.For(err) = %d, want 11; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "API access") {
		t.Errorf("403 error = %q, want it to mention 'API access'", err.Error())
	}
}

// TestSet_patch404_exit30_appsListHint asserts a 404 maps to exit 30 with
// the `gplay apps list` hint.
func TestSet_patch404_exit30_appsListHint(t *testing.T) {
	rt := &setRT{
		t:         t,
		editID:    "edit-404",
		patchCode: 404,
		patchResp: `{"error":{"code":404,"message":"not found"}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:         "com.example.app",
		ContactEmail:    "new@example.com",
		ContactEmailSet: true,
	})
	if got := exit.For(err); got != 30 {
		t.Errorf("exit.For(err) = %d, want 30; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "gplay apps list") {
		t.Errorf("404 error = %q, want it to mention 'gplay apps list'", err.Error())
	}
}

// TestNewSetCommand_registersExpectedFlags is a thin smoke test for the
// cobra wiring: every field flag exists, the write flags exist, there is
// NO --confirm (contact info is low-stakes/reversible), and the command
// is named "set".
func TestNewSetCommand_registersExpectedFlags(t *testing.T) {
	cmd := detailscmd.NewSetCommand(kernel.Boot{})
	for _, name := range []string{
		"package",
		"default-language",
		"contact-email",
		"contact-phone",
		"contact-website",
		"dry-run",
		"keep-edit-on-failure",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if cmd.Flags().Lookup("confirm") != nil {
		t.Error("cobra command has a --confirm flag; details set must NOT have one")
	}
	if got := cmd.Use; got != "set" {
		t.Errorf("cmd.Use = %q, want %q", got, "set")
	}
}

// TestSet_emitsConfirmationOnStderr asserts a committed details patch prints a
// single ✓ line on stderr (DESIGN §8) naming the package, alongside its stdout
// payload.
func TestSet_emitsConfirmationOnStderr(t *testing.T) {
	rt := &setRT{t: t, editID: "edit-set"}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:         "com.example.app",
		ContactEmail:    "new@example.com",
		ContactEmailSet: true,
	}); err != nil {
		t.Fatalf("RunSet: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "com.example.app") {
		t.Errorf("details set ✓ line wrong:\n%s", got)
	}
}

// TestSet_dryRun_noConfirmationOnStderr asserts --dry-run never emits a ✓.
func TestSet_dryRun_noConfirmationOnStderr(t *testing.T) {
	rt := &setRT{t: t}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := detailscmd.RunSet(rc, detailscmd.SetInput{
		Package:         "com.example.app",
		ContactEmail:    "new@example.com",
		ContactEmailSet: true,
		DryRun:          true,
	}); err != nil {
		t.Fatalf("RunSet: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}
