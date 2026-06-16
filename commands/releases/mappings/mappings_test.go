// Package mappings_test exercises `gplay releases mappings upload` at the
// kernel level: a RunContext built by hand, a RoundTripper injected via
// the oauth2.HTTPClient context key, and Run invoked directly. The
// RoundTripper sees both the /token exchange and the androidpublisher
// calls (edits.insert, deobfuscationfiles.upload, edits.commit/delete).
package mappings_test

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

	"github.com/PollyGlot/google-play-cli/commands/releases/mappings"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// mappingRT terminates the OAuth2 /token exchange and routes the Edit
// lifecycle calls a standalone mapping upload makes.
type mappingRT struct {
	t      *testing.T
	editID string

	mu        sync.Mutex
	calls     []string
	tokenHits int
}

func (r *mappingRT) RoundTrip(req *http.Request) (*http.Response, error) {
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
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/deobfuscationFiles/"):
		return jsonResp(200, `{"deobfuscationFile":{"symbolType":"proguard"}}`), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, r.editID)), nil
	}
	r.t.Fatalf("mappingRT: unexpected request: %s %s", req.Method, req.URL)
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

func writeFakeMapping(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mapping.txt")
	if err := os.WriteFile(p, []byte("com.example.Foo -> a.a:\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// TestRun_happyPath_beginUploadCommit_andConfirms asserts a standalone
// mapping upload opens its own Edit, POSTs the mapping keyed by the given
// --version-code + proguard, commits, and prints a ✓ confirmation (#250).
func TestRun_happyPath_beginUploadCommit_andConfirms(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt := &mappingRT{t: t, editID: "edit-map"}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := mappings.Run(rc, mappings.Input{
		Package:     "com.example.app",
		MappingPath: mapping,
		VersionCode: 142,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-map/apks/142/deobfuscationFiles/proguard",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-map:commit",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") {
		t.Errorf("missing ✓ confirmation; stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "142") {
		t.Errorf("✓ line should name versionCode 142; stderr=%q", stderr.String())
	}
}

// TestRun_missingVersionCode_exit2_noHTTP asserts --version-code is
// required: omitting it is a CLI misuse (exit 2) before any HTTP.
func TestRun_missingVersionCode_exit2_noHTTP(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt := &mappingRT{t: t}
	rc := newRC(t, rt)

	_, err := mappings.Run(rc, mappings.Input{
		Package:     "com.example.app",
		MappingPath: mapping,
	})
	if err == nil {
		t.Fatal("Run returned nil error with no --version-code")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("hit the network without --version-code: %v", rt.calls)
	}
}

// TestRun_nativeCodeType_inPath asserts --type nativeCode lands verbatim
// in the deobfuscationFiles path segment.
func TestRun_nativeCodeType_inPath(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt := &mappingRT{t: t, editID: "edit-map"}
	rc := newRC(t, rt)

	if _, err := mappings.Run(rc, mappings.Input{
		Package:     "com.example.app",
		MappingPath: mapping,
		VersionCode: 7,
		Type:        "nativeCode",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, c := range rt.calls {
		if strings.HasSuffix(c, "/apks/7/deobfuscationFiles/nativeCode") {
			found = true
		}
	}
	if !found {
		t.Errorf("no deobfuscationFiles/nativeCode call in %v", rt.calls)
	}
}

// TestRun_dryRun_noConfirmation_noHTTP asserts --dry-run validates the
// file and emits no ✓ and no network calls (not even the /token exchange).
func TestRun_dryRun_noConfirmation_noHTTP(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt := &mappingRT{t: t}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := mappings.Run(rc, mappings.Input{
		Package:     "com.example.app",
		MappingPath: mapping,
		VersionCode: 142,
		DryRun:      true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓; stderr=%q", stderr.String())
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run hit the network: %v", rt.calls)
	}
}

// TestNewCommand_registersExpectedFlags is a thin smoke test for the
// cobra wiring.
func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := mappings.NewCommand(kernel.Boot{})
	for _, name := range []string{
		"package",
		"version-code",
		"type",
		"keep-edit-on-failure",
		"dry-run",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "upload <mapping.txt>" {
		t.Errorf("cmd.Use = %q, want %q", got, "upload <mapping.txt>")
	}
}
