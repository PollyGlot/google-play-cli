package discard_test

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
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	discardcmd "github.com/PollyGlot/google-play-cli/commands/edits/discard"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const pkg = "com.example.app"

// discardRT serves the token exchange and the edits.delete DELETE; deleteStatus
// (0 → 204) forces a 404 for the already-gone test.
type discardRT struct {
	t            *testing.T
	deleteStatus int

	mu          sync.Mutex
	deleteCalls int
}

func (r *discardRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/") {
		r.deleteCalls++
		if r.deleteStatus != 0 {
			return jsonResp(r.deleteStatus, `{"error":{"code":404,"message":"edit not found"}}`), nil
		}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{
		"type": "service_account", "project_id": "p", "private_key": string(pemBytes),
		"client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token",
	})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, string) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	gplayDir := filepath.Join(t.TempDir(), ".gplay")
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Resolved = &config.Resolved{Pin: pkg, ProjectSharedPath: filepath.Join(gplayDir, "config.json")}
	return rc, gplayDir
}

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v (%T) has no ExitCode", err, err)
	}
	return c.ExitCode()
}

func TestRun_discardsAndClearsPin(t *testing.T) {
	rt := &discardRT{t: t}
	rc, gplayDir := newRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-3"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	if _, err := discardcmd.Run(rc, discardcmd.Input{Package: pkg}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.deleteCalls != 1 {
		t.Errorf("deleteCalls = %d, want 1", rt.deleteCalls)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("pin still present after discard")
	}
}

func TestRun_discard404_isSuccessAndClearsPin(t *testing.T) {
	// A 404 means the Edit is already gone: the discard's desired end state.
	// The pin must still be cleared and the command must succeed.
	rt := &discardRT{t: t, deleteStatus: 404}
	rc, gplayDir := newRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-gone"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	if _, err := discardcmd.Run(rc, discardcmd.Input{Package: pkg}); err != nil {
		t.Fatalf("a 404 discard should succeed: %v", err)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("pin must be cleared even when the Edit was already gone")
	}
}

func TestRun_discardServerError_clearsPinButReturnsError(t *testing.T) {
	// A non-404 server error: the user asked to abandon the Edit, so the local
	// pin must still be cleared (it auto-expires in ~24h), but the failed remote
	// discard must surface: a filesystem clear must not mask it.
	rt := &discardRT{t: t, deleteStatus: 500}
	rc, gplayDir := newRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-stuck"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	_, err := discardcmd.Run(rc, discardcmd.Input{Package: pkg})
	if err == nil {
		t.Fatal("a non-404 remote discard error must surface")
	}
	if rt.deleteCalls != 1 {
		t.Errorf("deleteCalls = %d, want 1: the remote discard must be attempted before the pin is cleared", rt.deleteCalls)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("the pin must be cleared even when the remote discard failed")
	}
}

func TestRun_noOpenEdit_exit60_noNetwork(t *testing.T) {
	rt := &discardRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := discardcmd.Run(rc, discardcmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 60 {
		t.Fatalf("exit = %d, want 60 (no open edit)", code)
	}
	if rt.deleteCalls != 0 {
		t.Errorf("no open edit must fail before the network; deleteCalls = %d", rt.deleteCalls)
	}
}
