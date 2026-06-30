package commit_test

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

	commitcmd "github.com/PollyGlot/google-play-cli/commands/edits/commit"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const pkg = "com.example.app"

// commitRT serves the token exchange and the edits.commit POST; commitStatus
// (0 → 200) forces a non-2xx for the failure test.
type commitRT struct {
	t            *testing.T
	commitStatus int

	mu          sync.Mutex
	commitCalls int
	insertCalls int
}

func (r *commitRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	if strings.HasSuffix(req.URL.Path, ":commit") {
		r.commitCalls++
		if r.commitStatus != 0 {
			return jsonResp(r.commitStatus, `{"error":{"code":400,"message":"validation failed"}}`), nil
		}
		return jsonResp(200, `{"id":"edit-9","expiryTimeSeconds":"0"}`), nil
	}
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits") {
		r.insertCalls++
		return jsonResp(200, `{"id":"edit-new"}`), nil
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

func TestRun_commitsAndClearsPin(t *testing.T) {
	rt := &commitRT{t: t}
	rc, gplayDir := newRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	if _, err := commitcmd.Run(rc, commitcmd.Input{Package: pkg}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.commitCalls != 1 {
		t.Errorf("commitCalls = %d, want 1", rt.commitCalls)
	}
	// Explicit-lifecycle contract: commit must never open a replacement Edit.
	if rt.insertCalls != 0 {
		t.Errorf("commit opened %d Edit(s); want 0 (it commits the pinned Edit)", rt.insertCalls)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("pin still present after a successful commit")
	}
}

func TestRun_noOpenEdit_exit60_noNetwork(t *testing.T) {
	rt := &commitRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := commitcmd.Run(rc, commitcmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 60 {
		t.Fatalf("exit = %d, want 60 (no open edit)", code)
	}
	if rt.commitCalls != 0 {
		t.Errorf("no open edit must fail before the network; commitCalls = %d", rt.commitCalls)
	}
}

func TestRun_commitFails_leavesPinInPlace(t *testing.T) {
	rt := &commitRT{t: t, commitStatus: 400}
	rc, gplayDir := newRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	if _, err := commitcmd.Run(rc, commitcmd.Input{Package: pkg}); err == nil {
		t.Fatal("expected a commit error")
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); !ok {
		t.Error("a failed commit must leave the pin in place for a retry/discard")
	}
}
