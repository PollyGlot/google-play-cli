package begin_test

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
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	begincmd "github.com/PollyGlot/google-play-cli/commands/edits/begin"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const pkg = "com.example.app"

// beginRT serves the OAuth token exchange, the edits.insert POST, and the
// edits.delete DELETE (the rollback path when the pin write fails).
type beginRT struct {
	t      *testing.T
	editID string

	mu          sync.Mutex
	insertCalls int
	deleteCalls int
}

func (r *beginRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits") {
		r.insertCalls++
		return jsonResp(200, `{"id":"`+r.editID+`","expiryTimeSeconds":"1700000000"}`), nil
	}
	if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/") {
		r.deleteCalls++
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

// failWriteFS is an OSFS whose WriteFile always fails, to drive begin's
// pin-write-failure rollback (it must discard the just-opened Edit).
type failWriteFS struct{ config.FS }

func (failWriteFS) WriteFile(string, []byte, fs.FileMode) error {
	return errors.New("disk full")
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

// newRC wires a RunContext routed through rt, pinned to a project whose .gplay/
// is dir, and returns it plus the resolved .gplay/ directory.
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

func TestRun_opensEditAndWritesPin(t *testing.T) {
	rt := &beginRT{t: t, editID: "edit-42"}
	rc, gplayDir := newRC(t, rt)

	r, err := begincmd.Run(rc, begincmd.Input{Package: pkg})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.insertCalls != 1 {
		t.Errorf("insertCalls = %d, want 1", rt.insertCalls)
	}
	pin, ok, err := editpin.Lookup(config.OSFS{}, gplayDir, pkg)
	if err != nil || !ok {
		t.Fatalf("pin not written: ok=%v err=%v", ok, err)
	}
	if pin.EditID != "edit-42" {
		t.Errorf("pinned editId = %q, want edit-42", pin.EditID)
	}
	// JSON payload mirrors the local pin state.
	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatJSON, r.Renderers()); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), `"editId": "edit-42"`) || !strings.Contains(buf.String(), `"open": true`) {
		t.Errorf("json payload = %s", buf.String())
	}
}

func TestRun_alreadyOpen_exit60_noNetwork(t *testing.T) {
	rt := &beginRT{t: t, editID: "edit-new"}
	rc, gplayDir := newRC(t, rt)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-already"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	_, err := begincmd.Run(rc, begincmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 60 {
		t.Fatalf("exit = %d, want 60 (already open)", code)
	}
	if rt.insertCalls != 0 {
		t.Errorf("a second begin must not open another Edit; insertCalls = %d", rt.insertCalls)
	}
}

func TestRun_pinWriteFailure_discardsServerEdit(t *testing.T) {
	// OpenExplicit succeeds, but persisting the pin fails. begin must roll back
	// by discarding the just-opened server-side Edit so no orphan is left, and
	// must leave no pin behind.
	rt := &beginRT{t: t, editID: "edit-orphan"}
	rc, gplayDir := newRC(t, rt)
	rc.FS = failWriteFS{config.OSFS{}}

	_, err := begincmd.Run(rc, begincmd.Input{Package: pkg})
	if err == nil {
		t.Fatal("expected the pin-write failure to surface")
	}
	if rt.insertCalls != 1 {
		t.Errorf("insertCalls = %d, want 1 (the Edit was opened)", rt.insertCalls)
	}
	if rt.deleteCalls != 1 {
		t.Errorf("deleteCalls = %d, want 1 (the opened Edit must be discarded on pin-write failure)", rt.deleteCalls)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("a failed begin must leave no pin behind")
	}
}

func TestRun_noProject_exit2(t *testing.T) {
	rt := &beginRT{t: t, editID: "edit-x"}
	rc, _ := newRC(t, rt)
	rc.Resolved = &config.Resolved{Pin: pkg} // no ProjectSharedPath → no .gplay/

	_, err := begincmd.Run(rc, begincmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 2 {
		t.Fatalf("exit = %d, want 2 (no project)", code)
	}
	if rt.insertCalls != 0 {
		t.Errorf("no project must fail before the network; insertCalls = %d", rt.insertCalls)
	}
}
