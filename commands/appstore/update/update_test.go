package update_test

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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	updatecmd "github.com/PollyGlot/google-play-cli/commands/appstore/update"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// testRoundTripper answers the /token exchange and the updateAppStoreHostedApp
// call, recording the request shape. status/resp are configurable so the
// refusal paths can return a 403/404. Nothing here touches the network.
type testRoundTripper struct {
	mu     sync.Mutex
	calls  []string
	apiURL string
	method string
	body   []byte
	status int
	resp   string
}

func (r *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.apiURL = req.URL.String()
	r.method = req.Method
	if req.Body != nil {
		r.body, _ = io.ReadAll(req.Body)
	}
	if r.status != 0 {
		return jsonResp(r.status, r.resp), nil
	}
	resp := r.resp
	if resp == "" {
		resp = `{}`
	}
	return jsonResp(200, resp), nil
}

// apiCalls counts the requests that were NOT the token exchange — the number
// that matters when asserting "no HTTP happened".
func (r *testRoundTripper) apiCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c != "POST /token" {
			n++
		}
	}
	return n
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper, stdin string) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stdin: strings.NewReader(stdin)}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// fullBody is a complete submission exercising every branch of the payload,
// including a policy response variant gplay does not model.
const fullBody = `{
  "appDetails": {"developerName": "Acme", "contactEmail": "dev@acme.test", "developerWebsite": "https://acme.test"},
  "activeApks": {"activeApkSets": [{"baseApkId": "apk-base", "splitApkId": ["apk-en", "apk-fr"]}]},
  "activeLocalizedStoreListings": [
    {"languageCode": "en-US", "appName": "Acme", "fullDescription": "long", "appIconId": "img-icon", "screenshotId": ["img-1", "img-2"]},
    {"languageCode": "fr-FR", "appName": "Acme", "fullDescription": "longue", "appIconId": "img-icon", "screenshotId": ["img-1"]}
  ],
  "policyDeclarations": [
    {"declarationId": "decl-1", "responses": [{"questionId": "q1", "documentResponse": {"fileId": "file-9"}}]}
  ]
}`

func writeBody(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "submission.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestRun_requestShape asserts the command emits the submit call on the app
// store axis and forwards the whole declarative body, field names included.
func TestRun_requestShape(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt, "")

	if _, err := updatecmd.Run(rc, updatecmd.Input{
		StorePackage: "com.example.store",
		Package:      "com.example.app",
		File:         writeBody(t, fullBody),
		Confirm:      true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if rt.method != http.MethodPost {
		t.Errorf("method = %q, want POST", rt.method)
	}
	if !strings.HasSuffix(rt.apiURL, "/appstore/com.example.store/apps:update") {
		t.Errorf("url %q is not the updateAppStoreHostedApp endpoint", rt.apiURL)
	}
	if strings.Contains(rt.apiURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", rt.apiURL)
	}

	var sent map[string]any
	if err := json.Unmarshal(rt.body, &sent); err != nil {
		t.Fatalf("request body %q is not JSON: %v", rt.body, err)
	}
	for _, key := range []string{"packageName", "appDetails", "activeApks", "activeLocalizedStoreListings", "policyDeclarations"} {
		if _, ok := sent[key]; !ok {
			t.Errorf("request body dropped the %q field: %s", key, rt.body)
		}
	}
	// The media ids are the whole point of the upload verbs: they must survive
	// the round trip through the CLI untouched, under the API's own spelling.
	for _, want := range []string{`"baseApkId":"apk-base"`, `"splitApkId":["apk-en","apk-fr"]`, `"appIconId":"img-icon"`, `"screenshotId":["img-1","img-2"]`} {
		if !strings.Contains(string(rt.body), want) {
			t.Errorf("request body is missing %s: %s", want, rt.body)
		}
	}
}

// TestRun_unknownPolicyVariantSurvives is the reason PolicyDeclaration.Responses
// stays json.RawMessage: Google keeps adding question types, and a response
// shape gplay has never seen must still reach the API intact.
func TestRun_unknownPolicyVariantSurvives(t *testing.T) {
	rt := &testRoundTripper{}
	body := `{"policyDeclarations":[{"declarationId":"d","responses":[{"questionId":"q","futureResponse":{"shape":"unknown","depth":[1,2]}}]}]}`

	if _, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		StorePackage: "s", Package: "com.example.app", File: writeBody(t, body), Confirm: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(rt.body), `"futureResponse":{"shape":"unknown","depth":[1,2]}`) {
		t.Errorf("an unmodelled policy response variant was dropped: %s", rt.body)
	}
}

// TestRun_readsStdin covers the pipe path: `--file -` and an omitted --file both
// read the body from stdin, so a CI job can generate the submission inline.
func TestRun_readsStdin(t *testing.T) {
	for _, file := range []string{"", "-"} {
		t.Run("file="+file, func(t *testing.T) {
			rt := &testRoundTripper{}
			rc := newRC(t, rt, fullBody)
			if _, err := updatecmd.Run(rc, updatecmd.Input{StorePackage: "s", Package: "com.example.app", File: file, Confirm: true}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !strings.Contains(string(rt.body), `"developerName":"Acme"`) {
				t.Errorf("the body piped on stdin did not reach the wire: %s", rt.body)
			}
		})
	}
}

// TestRun_missingConfirm_exit3 is the gate: submission is immediate and cannot
// be recalled, so the run must refuse — before any HTTP — naming the flag.
func TestRun_missingConfirm_exit3(t *testing.T) {
	rt := &testRoundTripper{}
	_, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		StorePackage: "s", Package: "com.example.app", File: writeBody(t, fullBody),
	})
	assertExit(t, err, 3)
	if !strings.Contains(err.Error(), "confirm") {
		t.Errorf("error %q does not name the --confirm flag an agent must pass", err)
	}
	if rt.apiCalls() != 0 {
		t.Errorf("a refused submission must emit no API call, got %v", rt.calls)
	}
}

// TestRun_dryRunNeedsNoConfirm: the rehearsal exists to be runnable without the
// gate. If --dry-run demanded --confirm there would be no way to check a
// submission before making it.
func TestRun_dryRunNeedsNoConfirm(t *testing.T) {
	rt := &testRoundTripper{}
	got, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		StorePackage: "com.example.store", Package: "com.example.app", File: writeBody(t, fullBody), DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry-run must not require --confirm: %v", err)
	}
	if rt.apiCalls() != 0 {
		t.Errorf("--dry-run must emit no API call, got %v", rt.calls)
	}

	// ADR-0017 §4: the gate is machine-readable, so an agent learns what the
	// live run will demand without failing once to find out.
	var buf bytes.Buffer
	if err := got.Renderers().JSON(&buf); err != nil {
		t.Fatalf("render JSON: %v", err)
	}
	var view struct {
		DryRun             bool     `json:"dryRun"`
		PackageName        string   `json:"packageName"`
		Locales            int      `json:"locales"`
		ApkSets            int      `json:"apkSets"`
		PolicyDeclarations int      `json:"policyDeclarations"`
		Requires           []string `json:"requires"`
	}
	if err := json.Unmarshal(buf.Bytes(), &view); err != nil {
		t.Fatalf("dry-run JSON %q: %v", buf.String(), err)
	}
	if !view.DryRun || view.PackageName != "com.example.app" {
		t.Errorf("dry-run view = %+v, want the resolved target", view)
	}
	if len(view.Requires) != 1 || view.Requires[0] != "confirm" {
		t.Errorf("requires = %v, want [confirm]", view.Requires)
	}
	// The counts are what makes a rehearsal worth reading: they prove the file
	// parsed the way the operator intended.
	if view.Locales != 2 || view.ApkSets != 1 || view.PolicyDeclarations != 1 {
		t.Errorf("counts = %d locales / %d apk sets / %d declarations, want 2/1/1", view.Locales, view.ApkSets, view.PolicyDeclarations)
	}
}

// TestRun_invalidJSON_exit2 keeps a malformed file client-side misuse.
func TestRun_invalidJSON_exit2(t *testing.T) {
	rt := &testRoundTripper{}
	_, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		StorePackage: "s", Package: "com.example.app", File: writeBody(t, `{"appDetails": `), Confirm: true,
	})
	assertExit(t, err, 2)
	if rt.apiCalls() != 0 {
		t.Errorf("a malformed body must not reach the API, got %v", rt.calls)
	}
}

// TestRun_emptyBody_exit2 covers the pipe that delivered nothing.
func TestRun_emptyBody_exit2(t *testing.T) {
	_, err := updatecmd.Run(newRC(t, &testRoundTripper{}, "   "), updatecmd.Input{
		StorePackage: "s", Package: "com.example.app", Confirm: true,
	})
	assertExit(t, err, 2)
}

// TestRun_unreadableFile_exit2 names which input path failed.
func TestRun_unreadableFile_exit2(t *testing.T) {
	_, err := updatecmd.Run(newRC(t, &testRoundTripper{}, ""), updatecmd.Input{
		StorePackage: "s", Package: "com.example.app", File: filepath.Join(t.TempDir(), "absent.json"), Confirm: true,
	})
	assertExit(t, err, 2)
}

// TestRun_bodyPackageContradictsFlag_exit2: this call submits to review, so a
// body naming a different app than the resolved target is refused rather than
// resolved one way silently.
func TestRun_bodyPackageContradictsFlag_exit2(t *testing.T) {
	rt := &testRoundTripper{}
	_, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		StorePackage: "s",
		Package:      "com.example.app",
		File:         writeBody(t, `{"packageName":"com.other.app","appDetails":{"developerName":"Acme"}}`),
		Confirm:      true,
	})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "com.other.app") || !strings.Contains(err.Error(), "com.example.app") {
		t.Errorf("error %q must name BOTH packages so the operator sees the contradiction", err)
	}
	if rt.apiCalls() != 0 {
		t.Errorf("the contradiction must be caught offline, got %v", rt.calls)
	}
}

// TestRun_bodyPackageAgrees accepts the redundant-but-consistent case.
func TestRun_bodyPackageAgrees(t *testing.T) {
	rt := &testRoundTripper{}
	if _, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		StorePackage: "s", Package: "com.example.app",
		File:    writeBody(t, `{"packageName":"com.example.app","appDetails":{"developerName":"Acme"}}`),
		Confirm: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(rt.body), `"packageName":"com.example.app"`) {
		t.Errorf("request body = %s, want the agreed package", rt.body)
	}
}

// TestRun_packageDefaultsToProjectPin: the repo pin resolves the target like
// everywhere else in the CLI.
func TestRun_packageDefaultsToProjectPin(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt, "")
	rc.Resolved = &config.Resolved{Pin: "com.pinned.app"}

	if _, err := updatecmd.Run(rc, updatecmd.Input{StorePackage: "s", File: writeBody(t, fullBody), Confirm: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(rt.body), `"packageName":"com.pinned.app"`) {
		t.Errorf("request body = %s, want the project pin as the target", rt.body)
	}
}

// TestRun_missingStorePackage_exit2 pins the app-store axis as required: it has
// no project-pin fallback (ADR-0043).
func TestRun_missingStorePackage_exit2(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &testRoundTripper{}
	_, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		Package: "com.example.app", File: writeBody(t, fullBody), Confirm: true,
	})
	assertExit(t, err, 2)
	if rt.apiCalls() != 0 {
		t.Errorf("an unresolved store package must fail before any API call, got %v", rt.calls)
	}
}

// TestRun_storePackageEnvCascade: flag beats env (ADR-0043 §1).
func TestRun_storePackageEnvCascade(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "com.env.store")
	rt := &testRoundTripper{}
	if _, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		Package: "com.example.app", File: writeBody(t, fullBody), Confirm: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.apiURL, "/appstore/com.env.store/") {
		t.Errorf("url %q did not pick the store package up from the environment", rt.apiURL)
	}
}

// TestRun_jsonPassthrough: ADR-0003 — whatever the API says is what --output
// json prints, verbatim, fields gplay does not model included.
func TestRun_jsonPassthrough(t *testing.T) {
	rt := &testRoundTripper{resp: `{"reviewId":"rev-1","unmodelled":{"x":1}}`}
	got, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		StorePackage: "s", Package: "com.example.app", File: writeBody(t, fullBody), Confirm: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := got.Renderers().JSON(&buf); err != nil {
		t.Fatalf("render JSON: %v", err)
	}
	if buf.String() != `{"reviewId":"rev-1","unmodelled":{"x":1}}` {
		t.Errorf("JSON = %s, want the verbatim API body", buf.String())
	}
}

// TestRun_jsonEmptyBodyFallback: the documented response models no fields, so a
// server answering with nothing would leave --output json (the CI default) with
// zero bytes to parse. A gplay-shaped object stands in.
func TestRun_jsonEmptyBodyFallback(t *testing.T) {
	rt := &testRoundTripper{resp: " "}
	got, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		StorePackage: "com.example.store", Package: "com.example.app", File: writeBody(t, fullBody), Confirm: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := got.Renderers().JSON(&buf); err != nil {
		t.Fatalf("render JSON: %v", err)
	}
	var view struct {
		OK              bool   `json:"ok"`
		PackageName     string `json:"packageName"`
		ReviewSubmitted bool   `json:"reviewSubmitted"`
		Locales         int    `json:"locales"`
	}
	if err := json.Unmarshal(buf.Bytes(), &view); err != nil {
		t.Fatalf("fallback JSON %q: %v", buf.String(), err)
	}
	if !view.OK || !view.ReviewSubmitted || view.PackageName != "com.example.app" || view.Locales != 2 {
		t.Errorf("fallback view = %+v, want the submission recap", view)
	}
}

// TestRun_humanViews: the table and markdown views must say the submission is
// gone to review — the fact an operator most needs to read back.
func TestRun_humanViews(t *testing.T) {
	got, err := updatecmd.Run(newRC(t, &testRoundTripper{}, ""), updatecmd.Input{
		StorePackage: "com.example.store", Package: "com.example.app", File: writeBody(t, fullBody), Confirm: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for name, render := range map[string]func(io.Writer) error{"table": got.Renderers().Table, "markdown": got.Renderers().Markdown} {
		var buf bytes.Buffer
		if err := render(&buf); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		out := buf.String()
		if !strings.Contains(out, "com.example.app") || !strings.Contains(out, "com.example.store") {
			t.Errorf("%s view does not name both identifiers: %s", name, out)
		}
		if !strings.Contains(strings.ToLower(out), "review") {
			t.Errorf("%s view does not say the app went to review: %s", name, out)
		}
	}
}

// TestRun_403_namesEnrollment / _404: upstream refusals keep the shared exit
// taxonomy and the namespace's hints.
func TestRun_403_exit11(t *testing.T) {
	rt := &testRoundTripper{status: http.StatusForbidden, resp: `{"error":{"message":"denied"}}`}
	_, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		StorePackage: "com.example.store", Package: "com.example.app", File: writeBody(t, fullBody), Confirm: true,
	})
	assertExit(t, err, 11)
	if !strings.Contains(err.Error(), "com.example.store") {
		t.Errorf("error %q does not name the app store whose enrollment is in question", err)
	}
}

func TestRun_404_exit30(t *testing.T) {
	rt := &testRoundTripper{status: http.StatusNotFound, resp: `{"error":{"message":"missing"}}`}
	_, err := updatecmd.Run(newRC(t, rt, ""), updatecmd.Input{
		StorePackage: "com.example.store", Package: "com.example.app", File: writeBody(t, fullBody), Confirm: true,
	})
	assertExit(t, err, 30)
}

// TestNewCommand_helpDocumentsTheVerb pins the public surface: the flags an
// agent must find, and the side effect it must not discover the hard way.
func TestNewCommand_helpDocumentsTheVerb(t *testing.T) {
	cmd := updatecmd.NewCommand(kernel.Boot{})
	if cmd.Use != "update" {
		t.Errorf("Use = %q, want update", cmd.Use)
	}
	if strings.TrimSpace(cmd.Short) == "" {
		t.Error("Short must not be empty")
	}
	for _, flag := range []string{"store-package", "package", "file", "confirm", "dry-run", "output"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("--%s is not declared", flag)
		}
	}
	// ADR-0043 amendment: the gate is --confirm, and no --yes alias exists.
	if cmd.Flags().Lookup("yes") != nil {
		t.Error("--yes must not exist: the CLI has exactly one gate name, --confirm")
	}
	long := strings.ToLower(cmd.Long)
	for _, phrase := range []string{"review", "confirm", "dry-run"} {
		if !strings.Contains(long, phrase) {
			t.Errorf("Long does not mention %q: %s", phrase, cmd.Long)
		}
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	if got := exit.For(err); got != want {
		t.Errorf("exit = %d, want %d (err: %v)", got, want, err)
	}
}
