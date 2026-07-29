// Package set_test (live half) exercises the #142 surface of `gplay
// compliance datasafety set`: the real POST, the --confirm gate, CI=true
// non-auto-confirm, the implicit-validate short-circuit on the confirm path,
// and the upstream-error exit-code mapping. It uses a mocked transport that
// terminates the OAuth2 /token exchange and the dataSafety POST (httptest
// style, like internal/play/*_test.go). Helpers exitCodeOf/writeCSV/validCSV
// live in set_test.go (same package).
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

	setcmd "github.com/PollyGlot/google-play-cli/commands/compliance/datasafety/set"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"golang.org/x/oauth2"
)

// liveRT terminates the /token exchange and the dataSafety POST. It captures
// the POST body and serves a configurable status/body so the success and
// error paths can be exercised.
type liveRT struct {
	t        *testing.T
	postCode int // 0 → 200
	postResp string

	mu       sync.Mutex
	calls    []string
	postBody []byte
}

func (r *liveRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return liveJSONResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}

	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/dataSafety") {
		if req.Body != nil {
			r.postBody, _ = io.ReadAll(req.Body)
		}
		code := r.postCode
		if code == 0 {
			code = 200
		}
		return liveJSONResp(code, r.postResp), nil
	}
	r.t.Fatalf("unexpected request (datasafety set is a single write-only POST): %s %s", req.Method, req.URL)
	return nil, nil
}

func (r *liveRT) posted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.Contains(c, "/dataSafety") {
			return true
		}
	}
	return false
}

func liveJSONResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func liveSAJSON(t *testing.T) []byte {
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

// newLiveRC builds a RunContext with a resolved Account and the mocked
// transport threaded through ctx (the kernel's test seam covers BOTH the
// /token exchange and the androidpublisher POST).
func newLiveRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(liveSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.AccountName = "ci-bot"
	rc.Resolved = &config.Resolved{}
	return rc
}

// TestConfirm_happyPath asserts a real `set --confirm` issues the /token
// exchange then the dataSafety POST carrying the CSV verbatim, and reports
// success; --output json passes the API response through.
func TestConfirm_happyPath(t *testing.T) {
	hdr := strings.Join([]string{"a", "b"}, ",")
	path := writeCSV(t, hdr+"\n1,2\n")
	rt := &liveRT{t: t, postResp: `{"safetyLabels":"echo"}`}
	rc := newLiveRC(t, rt)

	r, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path, Confirm: true})
	if err != nil {
		t.Fatalf("set --confirm: %v", err)
	}
	if !rt.posted() {
		t.Fatalf("expected a dataSafety POST; calls=%v", rt.calls)
	}

	var body struct {
		SafetyLabels string `json:"safetyLabels"`
	}
	if err := json.Unmarshal(rt.postBody, &body); err != nil {
		t.Fatalf("POST body not JSON: %v\nbody=%s", err, rt.postBody)
	}
	if !strings.Contains(body.SafetyLabels, "1,2") {
		t.Errorf("safetyLabels = %q, want the CSV contents", body.SafetyLabels)
	}

	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(js.String(), "echo") {
		t.Errorf("JSON output = %s, want the API response passed through", js.String())
	}
}

// TestConfirm_missing_refuses_exit3 asserts a real write without --confirm
// refuses with exit 3 (safety flag required, docs/DESIGN.md §9 — NOT the
// generic usage exit 2, #406), points at --dry-run, and makes NO POST.
func TestConfirm_missing_refuses_exit3(t *testing.T) {
	path, _, _ := validCSV(t, 1)
	rt := &liveRT{t: t}
	rc := newLiveRC(t, rt)

	_, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path})
	if got := exitCodeOf(t, err); got != 3 {
		t.Errorf("exit = %d, want 3 (missing --confirm); err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("refusal %q should point at --dry-run", err.Error())
	}
	if rt.posted() {
		t.Errorf("a refused write must not POST; calls=%v", rt.calls)
	}
}

// TestConfirm_missing_namesFlagInEnvelope asserts the refusal is a
// *exit.SafetyFlagError naming "confirm", so the --output json error envelope
// carries requires:["confirm"] and an agent can recover deterministically
// (ADR-0017 / ADR-0023).
func TestConfirm_missing_namesFlagInEnvelope(t *testing.T) {
	path, _, _ := validCSV(t, 1)
	rt := &liveRT{t: t}
	rc := newLiveRC(t, rt)

	_, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path})
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) {
		t.Fatalf("err = %T (%v), want *exit.SafetyFlagError", err, err)
	}
	if safety.Flag != "confirm" {
		t.Errorf("Flag = %q, want %q", safety.Flag, "confirm")
	}

	var buf bytes.Buffer
	if werr := output.WriteErrorEnvelope(&buf, err); werr != nil {
		t.Fatalf("WriteErrorEnvelope: %v", werr)
	}
	var env struct {
		Error struct {
			ExitCode int      `json:"exitCode"`
			Requires []string `json:"requires"`
		} `json:"error"`
	}
	if uerr := json.Unmarshal(buf.Bytes(), &env); uerr != nil {
		t.Fatalf("envelope not JSON: %v\n%s", uerr, buf.String())
	}
	if env.Error.ExitCode != 3 {
		t.Errorf("envelope exitCode = %d, want 3", env.Error.ExitCode)
	}
	if len(env.Error.Requires) != 1 || env.Error.Requires[0] != "confirm" {
		t.Errorf("envelope requires = %v, want [confirm]", env.Error.Requires)
	}
}

// TestConfirm_CI_neverAutoConfirms asserts CI=true does NOT auto-confirm: a
// missing --confirm still refuses (exit 3) and makes no POST, even under CI.
func TestConfirm_CI_neverAutoConfirms(t *testing.T) {
	t.Setenv("CI", "true")
	path, _, _ := validCSV(t, 1)
	rt := &liveRT{t: t}
	rc := newLiveRC(t, rt)

	_, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path})
	if got := exitCodeOf(t, err); got != 3 {
		t.Errorf("exit = %d, want 3 (CI must not auto-confirm); err=%v", got, err)
	}
	if rt.posted() {
		t.Errorf("CI=true must not POST without --confirm; calls=%v", rt.calls)
	}
}

// TestConfirm_invalidCSV_shortCircuits asserts a structurally invalid CSV
// fails (exit 20) before any POST, even with --confirm.
func TestConfirm_invalidCSV_shortCircuits(t *testing.T) {
	path := writeCSV(t, "a,b,c\nonly,two\n")
	rt := &liveRT{t: t}
	rc := newLiveRC(t, rt)

	_, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path, Confirm: true})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20 (structural validation); err=%v", got, err)
	}
	if rt.posted() {
		t.Errorf("an invalid CSV must not POST; calls=%v", rt.calls)
	}
}

// TestConfirm_apiErrors asserts upstream failures surface with the correct
// exit code (403→11 with API-access hint, 404→30 with apps-list hint,
// 500→40).
func TestConfirm_apiErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		code     int
		wantExit int
		wantHint string
	}{
		{"forbidden", 403, 11, "API access"},
		{"notFound", 404, 30, "gplay apps list"},
		{"serverError", 500, 40, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _, _ := validCSV(t, 1)
			rt := &liveRT{t: t, postCode: tc.code, postResp: `{"error":{"message":"nope"}}`}
			rc := newLiveRC(t, rt)

			_, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path, Confirm: true})
			if got := exitCodeOf(t, err); got != tc.wantExit {
				t.Errorf("exit = %d, want %d; err=%v", got, tc.wantExit, err)
			}
			if tc.wantHint != "" && !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("error %q should mention %q", err.Error(), tc.wantHint)
			}
		})
	}
}

// TestConfirm_emptyResponse_jsonSuccessObject asserts that when the API
// returns an empty 2xx body, --output json emits a gplay-shaped success
// object (the documented ADR-0003 exception) rather than zero bytes.
func TestConfirm_emptyResponse_jsonSuccessObject(t *testing.T) {
	path, _, _ := validCSV(t, 1)
	rt := &liveRT{t: t, postCode: 200, postResp: ""}
	rc := newLiveRC(t, rt)

	r, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path, Confirm: true})
	if err != nil {
		t.Fatalf("set --confirm: %v", err)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON render must not error on an empty API body: %v", err)
	}
	var out struct {
		OK      bool   `json:"ok"`
		Package string `json:"package"`
	}
	if err := json.Unmarshal(js.Bytes(), &out); err != nil {
		t.Fatalf("empty-response JSON is not a parseable success object: %v\nout=%s", err, js.String())
	}
	if !out.OK || out.Package != "com.example.app" {
		t.Errorf("success object = %s, want ok:true + package", js.String())
	}
}

// TestNewCommand_hasConfirm asserts the #142 flag surface adds --confirm.
func TestNewCommand_hasConfirm(t *testing.T) {
	cmd := setcmd.NewCommand(kernel.Boot{})
	if cmd.Flags().Lookup("confirm") == nil {
		t.Error("missing --confirm flag")
	}
}

// TestConfirm_emitsConfirmationOnStderr asserts a committed data-safety POST
// prints a single ✓ line on stderr (DESIGN §8) naming the package, alongside
// the stdout payload.
func TestConfirm_emitsConfirmationOnStderr(t *testing.T) {
	hdr := strings.Join([]string{"a", "b"}, ",")
	path := writeCSV(t, hdr+"\n1,2\n")
	rt := &liveRT{t: t, postResp: `{"safetyLabels":"echo"}`}
	rc := newLiveRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path, Confirm: true}); err != nil {
		t.Fatalf("set --confirm: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "com.example.app") {
		t.Errorf("datasafety set ✓ line wrong:\n%s", got)
	}
}

// TestDryRun_noConfirmationOnStderr asserts --dry-run never emits a ✓.
func TestDryRun_noConfirmationOnStderr(t *testing.T) {
	hdr := strings.Join([]string{"a", "b"}, ",")
	path := writeCSV(t, hdr+"\n1,2\n")
	rc, _ := newOfflineRC(t)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path, DryRun: true}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}
