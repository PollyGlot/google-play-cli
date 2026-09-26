package list_test

import (
	"bytes"
	"context"
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

	listcmd "github.com/PollyGlot/google-play-cli/commands/releases/artifacts/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/config/configtest"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const (
	apksBody    = `{"kind":"androidpublisher#apksListResponse","apks":[{"versionCode":7,"binary":{"sha1":"a1","sha256":"apk-7"}}]}`
	bundlesBody = `{"kind":"androidpublisher#bundlesListResponse","bundles":[{"versionCode":9,"sha256":"aab-9"},{"versionCode":5,"sha256":"aab-5"}]}`
)

// artRT fakes the Edit lifecycle plus both list endpoints and records the
// sequence of calls so tests can assert insert → lists → delete.
type artRT struct {
	mu    sync.Mutex
	calls []string
}

func (r *artRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := req.URL.Path
	switch {
	case req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(p, "/token"):
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	case req.Method == http.MethodPost && strings.HasSuffix(p, "/edits"):
		r.calls = append(r.calls, "POST /edits")
		return jsonResp(200, `{"id":"edit-ro","expiryTimeSeconds":"1700000000"}`), nil
	case req.Method == http.MethodDelete && strings.Contains(p, "/edits/"):
		r.calls = append(r.calls, "DELETE "+p)
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodGet && strings.HasSuffix(p, "/apks"):
		r.calls = append(r.calls, "GET "+p)
		return jsonResp(200, apksBody), nil
	case req.Method == http.MethodGet && strings.HasSuffix(p, "/bundles"):
		r.calls = append(r.calls, "GET "+p)
		return jsonResp(200, bundlesBody), nil
	}
	r.calls = append(r.calls, "UNEXPECTED "+req.Method+" "+p)
	return jsonResp(500, `{}`), nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key := testkit.RSAKey(t)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

func apiCalls(rt *artRT) []string {
	var out []string
	for _, c := range rt.calls {
		if c != "POST /token" {
			out = append(out, c)
		}
	}
	return out
}

// TestRun_bothKinds_readOnlyEdit_rowsByVersionCode asserts the lifecycle
// (insert → apks → bundles → delete, never commit), the merged table ordered
// by versionCode, and the {"apks","bundles"} JSON envelope of verbatim bodies.
func TestRun_bothKinds_readOnlyEdit_rowsByVersionCode(t *testing.T) {
	rt := &artRT{}
	rc := newRC(t, rt)
	r, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"POST /edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-ro/apks",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-ro/bundles",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-ro",
	}
	if got := apiCalls(rt); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", got, want)
	}

	var table bytes.Buffer
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("Table: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(table.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("table = %q, want header + 3 rows", table.String())
	}
	for i, prefix := range []string{"bundle", "apk", "bundle"} {
		if !strings.HasPrefix(lines[i+1], prefix) {
			t.Errorf("row %d = %q, want kind %s", i, lines[i+1], prefix)
		}
	}
	if !strings.Contains(lines[1], "5") || !strings.Contains(lines[2], "7") || !strings.Contains(lines[3], "9") {
		t.Errorf("rows not in versionCode order: %q", lines[1:])
	}

	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var env struct {
		Apks    json.RawMessage `json:"apks"`
		Bundles json.RawMessage `json:"bundles"`
	}
	if err := json.Unmarshal(js.Bytes(), &env); err != nil {
		t.Fatalf("json envelope: %v: %s", err, js.String())
	}
	if string(env.Apks) != apksBody || string(env.Bundles) != bundlesBody {
		t.Errorf("json values must be the verbatim API bodies: %s", js.String())
	}
}

// TestRun_kindApk_sendsOnlyApksRequest asserts --format apk skips bundles.list
// and passes the apks response through verbatim.
func TestRun_kindApk_sendsOnlyApksRequest(t *testing.T) {
	rt := &artRT{}
	rc := newRC(t, rt)
	r, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", Format: "apk"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range rt.calls {
		if strings.HasSuffix(c, "/bundles") {
			t.Errorf("--format apk must not call bundles.list; calls=%v", rt.calls)
		}
	}
	sawApks := false
	for _, c := range rt.calls {
		if strings.HasSuffix(c, "/apks") {
			sawApks = true
		}
	}
	if !sawApks {
		t.Errorf("apks.list not called; calls=%v", rt.calls)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if js.String() != apksBody {
		t.Errorf("json = %s, want the verbatim apks.list body", js.String())
	}
}

// TestRun_explicitPin_readsInsidePinnedEdit_noInsertNoDelete asserts a
// `gplay edits begin` pin is reused untouched: no insert, no delete.
func TestRun_explicitPin_readsInsidePinnedEdit_noInsertNoDelete(t *testing.T) {
	rt := &artRT{}
	rc := newRC(t, rt)
	fsys := configtest.NewMemFS("/repo", "/home")
	if err := editpin.Write(fsys, "/repo/.gplay", "com.example.app", "edit-pinned"); err != nil {
		t.Fatalf("editpin.Write: %v", err)
	}
	rc.FS = fsys
	rc.Resolved = &config.Resolved{ProjectSharedPath: "/repo/.gplay/config.json"}
	if _, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", Format: "bundle"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"GET /androidpublisher/v3/applications/com.example.app/edits/edit-pinned/bundles"}
	if got := apiCalls(rt); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v (pinned Edit reused, never inserted or discarded)", got, want)
	}
}

// TestRun_badKind_exit2_noNetwork asserts --format is validated before any HTTP.
func TestRun_badKind_exit2_noNetwork(t *testing.T) {
	rt := &artRT{}
	rc := newRC(t, rt)
	_, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", Format: "aab"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_noPackage_exit2 asserts the package resolution guard.
func TestRun_noPackage_exit2(t *testing.T) {
	rt := &artRT{}
	rc := newRC(t, rt)
	_, err := listcmd.Run(rc, listcmd.Input{})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
}
