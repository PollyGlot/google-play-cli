// Package upload_test exercises `gplay releases upload` at the kernel
// level: a RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. The
// RoundTripper sees both the /token exchange and the androidpublisher
// API calls, so a single seam proves the auth + Edit lifecycle wiring.
package upload_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// uploadRT is the RoundTripper command-level upload tests inject. It
// terminates the OAuth2 /token exchange (so token.Source produces a
// usable Bearer token) and routes every androidpublisher call needed
// by the orchestrator: edits.insert, bundles.upload, tracks.update,
// edits.commit, edits.delete. The fake response bodies are the minimum
// the orchestrator's response parsers accept.
//
// The recorded paths let tests assert that, e.g., a confirm-guarded
// production publish never hits the wire at all.
type uploadRT struct {
	t                  *testing.T
	editID             string
	versionCode        int
	trackUpdateRawResp string

	mu             sync.Mutex
	calls          []string
	tokenHits      int
	trackUpdateReq []byte
}

func (r *uploadRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		r.calls = append(r.calls, "POST /token")
		body := `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`
		return jsonResp(200, body), nil
	}

	r.calls = append(r.calls, req.Method+" "+req.URL.Path)

	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/bundles"):
		return jsonResp(200, fmt.Sprintf(`{"versionCode":%d,"sha1":"abc","sha256":"def"}`, r.versionCode)), nil
	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/tracks/"):
		body, _ := io.ReadAll(req.Body)
		r.trackUpdateReq = body
		resp := r.trackUpdateRawResp
		if resp == "" {
			resp = `{}`
		}
		return jsonResp(200, resp), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, r.editID)), nil
	}
	r.t.Fatalf("uploadRT: unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// signedSAJSON generates a service-account JSON whose private_key is a
// fresh RSA key so the oauth2 library can sign the exchange JWT in
// tests. Mirrors the helper in doctor_test.go.
func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, err := json.Marshal(map[string]any{
		"type":         "service_account",
		"project_id":   "test-proj",
		"private_key":  string(pemBytes),
		"client_email": "playci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// writeFakeAAB creates a non-empty file with a .aab extension. The
// RoundTripper does not validate the bytes — bundles.Upload just needs
// os.Open to succeed.
func writeFakeAAB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.aab")
	if err := os.WriteFile(p, []byte("fake-aab-content"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// newRC builds a RunContext with the injected RoundTripper installed as
// the oauth2.HTTPClient seam and a parsed Account loaded from
// signedSAJSON. The kernel-level Run is invoked directly so the test
// covers the full Run → orchestrator → play/* round trip.
func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, &stdout
}

// TestRun_internalTrack_happyPath_hitsTokenAndAndroidPublisher exercises
// the full vertical slice. Asserts both that the orchestrator returned
// a usable Renderable and that the injected RoundTripper saw the
// /token exchange in front of the androidpublisher calls.
func TestRun_internalTrack_happyPath_hitsTokenAndAndroidPublisher(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	}
	rc, _ := newRC(t, rt)

	r, err := upload.Run(rc, upload.Input{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on happy path")
	}

	if rt.tokenHits == 0 {
		t.Errorf("RoundTripper saw no /token exchange; calls=%v", rt.calls)
	}
	// The /token call must precede the androidpublisher edits.insert.
	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-xyz/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-xyz:commit",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}
}

// TestRun_mutualExclusion_releaseNotesAndDir_exit2_noHTTP asserts the
// CLI-misuse guard fires before any HTTP. The RoundTripper must record
// zero calls so a buggy fix that defers the check past auth setup is
// caught here.
func TestRun_mutualExclusion_releaseNotesAndDir_exit2_noHTTP(t *testing.T) {
	rt := &uploadRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := upload.Run(rc, upload.Input{
		Package:         "com.example.app",
		Track:           "internal",
		AABPath:         "/tmp/whatever.aab",
		ReleaseNotes:    "hello",
		ReleaseNotesDir: "/tmp/notes",
	})
	if err == nil {
		t.Fatal("Run returned nil error; want usage error")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("RoundTripper saw %d calls on a usage error: %v", len(rt.calls), rt.calls)
	}
}

// TestRun_productionPublishWithoutConfirm_exit2_noHTTP asserts that a
// production --complete without --confirm hits the orchestrator's
// ConfirmRequiredError guard before any HTTP — including the /token
// exchange. The oauth2 library is lazy, so a buggy refactor that
// pre-mints a token would show up as a non-zero call count here.
func TestRun_productionPublishWithoutConfirm_exit2_noHTTP(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &uploadRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := upload.Run(rc, upload.Input{
		Package:  "com.example.app",
		Track:    "production",
		AABPath:  aab,
		Complete: true,
		Confirm:  false,
	})
	if err == nil {
		t.Fatal("Run returned nil error; want ConfirmRequiredError")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("RoundTripper saw %d calls on confirm-guarded error: %v", len(rt.calls), rt.calls)
	}
}

// TestRun_productionPublishWithConfirm_succeedsAndHitsAPI asserts that
// --confirm unblocks the production publish and the RoundTripper sees
// the full Edit lifecycle on the production track. The tracks.update
// payload must carry status=completed and userFraction=1.0.
func TestRun_productionPublishWithConfirm_succeedsAndHitsAPI(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-prod",
		versionCode:        200,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"200","status":"completed","versionCodes":["200"],"userFraction":1.0}]}`,
	}
	rc, _ := newRC(t, rt)

	r, err := upload.Run(rc, upload.Input{
		Package:  "com.example.app",
		Track:    "production",
		AABPath:  aab,
		Complete: true,
		Confirm:  true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on confirmed production publish")
	}
	if rt.tokenHits == 0 {
		t.Errorf("RoundTripper saw no /token exchange; calls=%v", rt.calls)
	}
	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"track":"production"`) {
		t.Errorf("tracks.update body = %s, want track=production", body)
	}
	if !strings.Contains(body, `"status":"completed"`) {
		t.Errorf("tracks.update body = %s, want status=completed", body)
	}
	if !strings.Contains(body, `"userFraction":1`) {
		t.Errorf("tracks.update body = %s, want userFraction=1.0", body)
	}
}
