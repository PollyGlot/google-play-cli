package set_test

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
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	setcmd "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/set"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type efRT struct {
	t       *testing.T
	mu      sync.Mutex
	calls   []string
	putBody []byte
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
		return jsonResp(`{"id":"edit1"}`), nil
	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/expansionFiles/"):
		r.putBody, _ = io.ReadAll(req.Body)
		return jsonResp(`{"referencesVersion":140}`), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		return jsonResp(`{"id":"edit1"}`), nil
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

// TestRun_missingReferencesVersion_exit2 asserts --references-version is required.
func TestRun_missingReferencesVersion_exit2(t *testing.T) {
	r := &efRT{t: t}
	rc := newRC(t, r)
	_, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", r.calls)
	}
}

// TestRun_happyPath_putsReference asserts insert → PUT → commit with the body.
func TestRun_happyPath_putsReference(t *testing.T) {
	r := &efRT{t: t}
	rc := newRC(t, r)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	if _, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main", ReferencesVersion: 140}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit1/apks/142/expansionFiles/main",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit1:commit",
	}
	if strings.Join(r.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", r.calls, want)
	}
	if !strings.Contains(string(r.putBody), `"referencesVersion":140`) {
		t.Errorf("PUT body %q should set referencesVersion 140", r.putBody)
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") {
		t.Errorf("stderr missing ✓:\n%s", stderr.String())
	}
}
