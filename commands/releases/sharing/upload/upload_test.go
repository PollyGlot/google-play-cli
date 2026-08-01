// Package upload_test exercises `gplay releases sharing upload` at the kernel
// level: a RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// promote/rollout command test harness.
package upload_test

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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	uploadcmd "github.com/PollyGlot/google-play-cli/commands/releases/sharing/upload"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const artifactBody = `{"downloadUrl":"https://play.google.com/apps/test/abc123","certificateFingerprint":"AA:BB:CC","sha256":"deadbeef"}`

// sharingRT terminates the /token exchange and routes the single artifact
// upload POST, capturing the call sequence + the request URL.
type sharingRT struct {
	t *testing.T

	mu        sync.Mutex
	calls     []string
	tokenHits int
	uploadURL string
}

func (r *sharingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/internalappsharing/") {
		r.uploadURL = req.URL.String()
		return jsonResp(200, artifactBody), nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

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

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, &stdout
}

func writeArtifact(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("PK fake artifact"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	return p
}

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err = %v (%T), want an ExitCode()", err, err)
	}
	return c.ExitCode()
}

// TestRun_apk_happyPath asserts the /token exchange precedes a POST to the apk
// artifact endpoint, the JSON view passes the artifact through verbatim, and a
// ✓ line is written to stderr.
func TestRun_apk_happyPath(t *testing.T) {
	rt := &sharingRT{t: t}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: writeArtifact(t, "app.apk")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.tokenHits == 0 {
		t.Errorf("no /token exchange; calls=%v", rt.calls)
	}
	for _, want := range []string{"/applications/internalappsharing/com.example.app/artifacts/apk", "uploadType=media"} {
		if !strings.Contains(rt.uploadURL, want) {
			t.Errorf("upload url %q missing %q", rt.uploadURL, want)
		}
	}
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if strings.TrimSpace(jsonOut.String()) != artifactBody {
		t.Errorf("json = %s, want verbatim artifact pass-through", jsonOut.String())
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") || !strings.Contains(stderr.String(), "abc123") {
		t.Errorf("stderr missing ✓ line with the link:\n%s", stderr.String())
	}
}

// TestRun_aab_usesBundleEndpoint asserts a .aab routes to the bundle endpoint.
func TestRun_aab_usesBundleEndpoint(t *testing.T) {
	rt := &sharingRT{t: t}
	rc, _ := newRC(t, rt)
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: writeArtifact(t, "app.aab")}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.uploadURL, "/artifacts/bundle") {
		t.Errorf("upload url %q should hit the bundle endpoint", rt.uploadURL)
	}
}

// TestRun_formatOverride asserts --format forces the endpoint regardless of
// extension.
func TestRun_formatOverride(t *testing.T) {
	rt := &sharingRT{t: t}
	rc, _ := newRC(t, rt)
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: writeArtifact(t, "build.bin"), Format: "bundle"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.uploadURL, "/artifacts/bundle") {
		t.Errorf("upload url %q should honor --format bundle", rt.uploadURL)
	}
}

// TestRun_unknownExtension_exit20_noNetwork asserts an ambiguous extension with
// no --format fails offline with exit 20.
func TestRun_unknownExtension_exit20_noNetwork(t *testing.T) {
	rt := &sharingRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: writeArtifact(t, "build.bin")})
	if got := exitOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("unknown extension must make no network call; calls=%v", rt.calls)
	}
}

// TestRun_directory_exit20_noNetwork asserts a non-regular path fails offline.
func TestRun_directory_exit20_noNetwork(t *testing.T) {
	rt := &sharingRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: t.TempDir(), Format: "apk"})
	if got := exitOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("a directory must make no network call; calls=%v", rt.calls)
	}
}

// TestRun_missingPackage_exit2_noNetwork asserts a missing package is CLI misuse.
func TestRun_missingPackage_exit2_noNetwork(t *testing.T) {
	rt := &sharingRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := uploadcmd.Run(rc, uploadcmd.Input{ArtifactPath: writeArtifact(t, "app.apk")})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("missing package must make no network call; calls=%v", rt.calls)
	}
}

// TestRun_dryRun_noNetwork_noConfirmation asserts --dry-run validates offline,
// emits a dryRun JSON view, and never writes a ✓.
func TestRun_dryRun_noNetwork_noConfirmation(t *testing.T) {
	rt := &sharingRT{t: t}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: writeArtifact(t, "app.apk"), DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run must make no network call; calls=%v", rt.calls)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓; stderr=%q", stderr.String())
	}
	var view struct {
		DryRun  bool   `json:"dryRun"`
		Package string `json:"package"`
		Format  string `json:"format"`
	}
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &view); err != nil {
		t.Fatalf("dry-run json not parseable: %v", err)
	}
	if !view.DryRun || view.Package != "com.example.app" || view.Format != "apk" {
		t.Errorf("dry-run view = %+v, want dryRun apk for com.example.app", view)
	}
}

// TestNewCommand_flags asserts the flag/name surface. The [experimental] label
// is applied at registration by kernel.Experimental and pinned centrally by
// TestStabilityRegistry_pinsPublicContract (ADR-0010/ADR-0042).
func TestNewCommand_flags(t *testing.T) {
	cmd := uploadcmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"package", "format", "dry-run", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
}
