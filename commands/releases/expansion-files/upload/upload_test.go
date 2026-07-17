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

	uploadcmd "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/upload"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type efRT struct {
	t     *testing.T
	mu    sync.Mutex
	calls []string
}

func (r *efRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(`{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(`{"id":"edit1","expiryTimeSeconds":"1700000000"}`), nil
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/expansionFiles/"):
		// Resumable initiate: session URI in Location; the PUT chunk below
		// carries the .obb bytes and returns the resource body.
		loc := req.URL.Scheme + "://" + req.URL.Host + req.URL.Path + "?upload_id=session-1"
		return &http.Response{StatusCode: 200, Header: http.Header{"Location": []string{loc}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/expansionFiles/"):
		return jsonResp(`{"expansionFile":{"fileSize":"123"}}`), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		return jsonResp(`{"id":"edit1"}`), nil
	case req.Method == http.MethodDelete:
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func saJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, transport http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: transport})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

func writeOBB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "assets.obb")
	if err := os.WriteFile(p, []byte("fake obb"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err = %v (%T), want ExitCode()", err, err)
	}
	return c.ExitCode()
}

// TestRun_happyPath_editSequence asserts insert → upload → commit and a verbatim
// pass-through, with a ✓.
func TestRun_happyPath_editSequence(t *testing.T) {
	r := &efRT{t: t}
	rc := newRC(t, r)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	res, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main", OBBPath: writeOBB(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit1/apks/142/expansionFiles/main",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit1/apks/142/expansionFiles/main",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit1:commit",
	}
	if len(r.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", r.calls, want)
	}
	for i := range want {
		if r.calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, r.calls[i], want[i])
		}
	}
	var out bytes.Buffer
	if err := res.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"fileSize":"123"`) {
		t.Errorf("json %s should pass the upload response through", out.String())
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") {
		t.Errorf("stderr missing ✓:\n%s", stderr.String())
	}
}

// TestRun_missingVersionCode_exit2_noNetwork.
func TestRun_missingVersionCode_exit2_noNetwork(t *testing.T) {
	r := &efRT{t: t}
	rc := newRC(t, r)
	_, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", Type: "main", OBBPath: writeOBB(t)})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if len(r.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", r.calls)
	}
}

// TestRun_badType_exit2_noNetwork.
func TestRun_badType_exit2_noNetwork(t *testing.T) {
	r := &efRT{t: t}
	rc := newRC(t, r)
	_, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "nope", OBBPath: writeOBB(t)})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if len(r.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", r.calls)
	}
}

// TestRun_dryRun_validatesFile_noNetwork: a directory path fails offline (exit
// 20); a good file rehearses with no network and no ✓.
func TestRun_dryRun_validatesFile_noNetwork(t *testing.T) {
	r := &efRT{t: t}
	rc := newRC(t, r)
	// directory → exit 20
	if got := exitOf(t, mustErr(uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main", OBBPath: t.TempDir(), DryRun: true}))); got != 20 {
		t.Errorf("directory dry-run exit = %d, want 20", got)
	}
	if len(r.calls) != 0 {
		t.Errorf("dry-run must make no network call; calls=%v", r.calls)
	}
	// good file → rehearses, no network
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main", OBBPath: writeOBB(t), DryRun: true}); err != nil {
		t.Fatalf("dry-run good file: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("dry-run must make no network call; calls=%v", r.calls)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓; stderr=%q", stderr.String())
	}
}

func mustErr(_ output.Renderable, err error) error { return err }
