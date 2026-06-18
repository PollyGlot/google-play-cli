package view_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	viewcmd "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/view"
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
		return jsonResp(`{"id":"edit1"}`), nil
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/expansionFiles/"):
		return jsonResp(`{"referencesVersion":140}`), nil
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

// TestRun_readOnlyEditSequence asserts insert → get → discard and the parsed
// referencesVersion surfaces in the table while JSON is verbatim.
func TestRun_readOnlyEditSequence(t *testing.T) {
	r := &efRT{t: t}
	rc := newRC(t, r)
	res, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit1/apks/142/expansionFiles/main",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit1",
	}
	if strings.Join(r.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", r.calls, want)
	}
	var tbl bytes.Buffer
	if err := res.Renderers().Table(&tbl); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(tbl.String(), "referencesVersion: 140") {
		t.Errorf("table %q should show referencesVersion 140", tbl.String())
	}
	if strings.Contains(tbl.String(), "fileSize") {
		t.Errorf("table should not show fileSize when only referencesVersion is set: %q", tbl.String())
	}
	var js bytes.Buffer
	if err := res.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(js.String(), `"referencesVersion":140`) {
		t.Errorf("json %q should be verbatim", js.String())
	}
}
