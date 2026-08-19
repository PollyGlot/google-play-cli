package publishstatus_test

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

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	publishstatuscmd "github.com/PollyGlot/google-play-cli/commands/appstore/publishstatus"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

// testRoundTripper answers the /token exchange and the
// updateAppStoreHostedAppPublishStatus call, recording the request shape.
// status/body are configurable so the refusal paths can return a 403/404.
// Nothing here touches the network.
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

// sentState unmarshals the recorded request body and returns the publishState
// the command put on the wire.
func sentState(t *testing.T, body []byte) string {
	t.Helper()
	var sent struct {
		PublishState string `json:"publishState"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("request body %q is not JSON: %v", body, err)
	}
	return sent.PublishState
}

// TestRun_requestShape asserts both states reach the publish-status endpoint
// with the exact API enum: POST to
// appstore/{sp}/apps/{pkg}:updateAppStoreHostedAppPublishStatus, no Edit.
func TestRun_requestShape(t *testing.T) {
	for word, want := range map[string]string{
		"published":   appstore.PublishStatePublished,
		"unpublished": appstore.PublishStateUnpublished,
	} {
		t.Run(word, func(t *testing.T) {
			rt := &testRoundTripper{}
			rc := newRC(t, rt)

			if _, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example.store", Package: "com.example.app", State: word}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if rt.method != http.MethodPost {
				t.Errorf("method = %q, want POST", rt.method)
			}
			if !strings.HasSuffix(rt.apiURL, "/appstore/com.example.store/apps/com.example.app:updateAppStoreHostedAppPublishStatus") {
				t.Errorf("url %q is not the updateAppStoreHostedAppPublishStatus endpoint", rt.apiURL)
			}
			if strings.Contains(rt.apiURL, "/edits/") {
				t.Errorf("url %q must not open an Edit", rt.apiURL)
			}
			if got := sentState(t, rt.body); got != want {
				t.Errorf("body publishState = %q, want %q", got, want)
			}
		})
	}
}

// TestRun_stateCaseInsensitive asserts the human word is matched
// case-insensitively: an agent emitting `UNPUBLISHED` must not be refused,
// while the wire still carries the exact API enum.
func TestRun_stateCaseInsensitive(t *testing.T) {
	for _, word := range []string{"UNPUBLISHED", "Unpublished", "  unPUBLISHed  "} {
		rt := &testRoundTripper{}
		rc := newRC(t, rt)

		if _, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example.store", Package: "com.example.app", State: word}); err != nil {
			t.Fatalf("Run(%q): %v", word, err)
		}
		if got := sentState(t, rt.body); got != appstore.PublishStateUnpublished {
			t.Errorf("Run(%q): body publishState = %q, want %q", word, got, appstore.PublishStateUnpublished)
		}
	}
}

// TestRun_invalidState_exit2 asserts an unknown word is CLI misuse, refused
// before any HTTP call, with an error that enumerates the accepted values:
// never a server-side 400.
func TestRun_invalidState_exit2(t *testing.T) {
	for _, word := range []string{"draft", "", "APP_STORE_APP_PUBLISH_STATE_UNSPECIFIED"} {
		rt := &testRoundTripper{}
		rc := newRC(t, rt)

		_, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example.store", Package: "com.example.app", State: word})
		assertExit(t, err, 2)
		for _, want := range []string{"published", "unpublished"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Run(%q): error %q should enumerate %q", word, err, want)
			}
		}
		if len(rt.calls) != 0 {
			t.Errorf("Run(%q): validation must fail before any HTTP call, got %v", word, rt.calls)
		}
	}
}

// TestRun_storePackagePathEscaped asserts both addressing values are
// path-escaped on the way into the URL.
func TestRun_storePackagePathEscaped(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	if _, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example store", Package: "com.example app", State: "published"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(rt.apiURL, " ") {
		t.Errorf("url %q did not escape the package names", rt.apiURL)
	}
	if !strings.Contains(rt.apiURL, "com.example%20store") || !strings.Contains(rt.apiURL, "com.example%20app") {
		t.Errorf("url %q missing the escaped package names", rt.apiURL)
	}
}

// TestRun_packageDefaultsToProjectPin asserts an omitted --package falls back to
// the repo's .gplay/config.json pin, like every other package-axis command.
func TestRun_packageDefaultsToProjectPin(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)
	rc.Resolved = &config.Resolved{Pin: "com.pinned.app"}

	if _, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example.store", State: "published"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.apiURL, "/apps/com.pinned.app:") {
		t.Errorf("url %q should target the project pin com.pinned.app", rt.apiURL)
	}
}

// TestRun_jsonPassthrough asserts --output json emits the API response verbatim
// (ADR-0003), including fields the empty response schema does not model.
func TestRun_jsonPassthrough(t *testing.T) {
	const body = `{"unmodeledFutureField":"kept"}`
	rt := &testRoundTripper{resp: body}
	rc := newRC(t, rt)

	r, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example.store", Package: "com.example.app", State: "unpublished"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if out.String() != body {
		t.Errorf("json = %q, want the verbatim API response %q", out.String(), body)
	}
}

// TestRun_jsonEmptyBodyFallback asserts an empty API body still yields parseable
// JSON: the documented ADR-0003 exception (as in `appstore create`), never zero
// bytes on the CI default format, and that it names the applied state.
func TestRun_jsonEmptyBodyFallback(t *testing.T) {
	rt := &testRoundTripper{status: 200, resp: ""}
	rc := newRC(t, rt)

	r, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example.store", Package: "com.example.app", State: "unpublished"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got struct {
		OK                  bool   `json:"ok"`
		AppStorePackageName string `json:"appStorePackageName"`
		PackageName         string `json:"packageName"`
		PublishState        string `json:"publishState"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json %q is not parseable: %v", out.String(), err)
	}
	if !got.OK || got.AppStorePackageName != "com.example.store" || got.PackageName != "com.example.app" || got.PublishState != appstore.PublishStateUnpublished {
		t.Errorf("fallback json = %+v", got)
	}
}

// TestRun_humanViews asserts the table and markdown views name both identifiers
// and the applied enum: the response carries no fields, so the echo is the
// whole view.
func TestRun_humanViews(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	r, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example.store", Package: "com.example.app", State: "unpublished"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for name, render := range map[string]func(io.Writer) error{
		"table":    r.Renderers().Table,
		"markdown": r.Renderers().Markdown,
	} {
		var out bytes.Buffer
		if err := render(&out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, want := range []string{"com.example.store", "com.example.app", appstore.PublishStateUnpublished} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s view %q missing %q", name, out.String(), want)
			}
		}
	}
}

// TestRun_dryRun_noHTTP asserts --dry-run resolves the target, performs no HTTP
// call at all (not even the token exchange), and renders the ADR-0017 preview:
// the human views say "would set", the JSON view carries dryRun plus the
// (empty) machine-readable requires array: empty because the flip is
// reversible and gated by no safety flag.
func TestRun_dryRun_noHTTP(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	r, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example.store", Package: "com.example.app", State: "unpublished", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("--dry-run must not perform any HTTP call, got %v", rt.calls)
	}

	var table bytes.Buffer
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("table: %v", err)
	}
	for _, want := range []string{"would set", "(dry-run)", "com.example.store", "com.example.app", "unpublished"} {
		if !strings.Contains(table.String(), want) {
			t.Errorf("table view %q missing %q", table.String(), want)
		}
	}

	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got struct {
		DryRun              bool      `json:"dryRun"`
		AppStorePackageName string    `json:"appStorePackageName"`
		PackageName         string    `json:"packageName"`
		PublishState        string    `json:"publishState"`
		Requires            *[]string `json:"requires"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json %q is not parseable: %v", out.String(), err)
	}
	if !got.DryRun || got.AppStorePackageName != "com.example.store" || got.PackageName != "com.example.app" || got.PublishState != appstore.PublishStateUnpublished {
		t.Errorf("dry-run json = %+v", got)
	}
	if got.Requires == nil || len(*got.Requires) != 0 {
		t.Errorf("dry-run json %q must carry an empty requires array, got %v", out.String(), got.Requires)
	}
}

// TestRun_dryRun_validatesFirst asserts --dry-run still fails fast on an
// unresolvable target: the rehearsal previews a real call, not a hypothetical.
func TestRun_dryRun_validatesFirst(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	_, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{Package: "com.example.app", State: "published", DryRun: true})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("validation must fail before any HTTP call, got %v", rt.calls)
	}
}

// TestRun_missingStorePackage_exit2 asserts the app store package name is
// required and its absence is CLI misuse: it identifies the caller and has no
// project-level default (the Project pin pins a package, never a store).
func TestRun_missingStorePackage_exit2(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	_, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{Package: "com.example.app", State: "published"})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "store-package") {
		t.Errorf("error %q should name the --store-package flag", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("validation must fail before any HTTP call, got %v", rt.calls)
	}
}

// TestRun_storePackageEnvCascade asserts the ADR-0043 cascade: the
// --store-package flag wins, $GPLAY_APP_STORE_PACKAGE fills in when the flag is
// absent, so a CI job exports the store identity once for the whole namespace.
func TestRun_storePackageEnvCascade(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "com.env.store")
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	if _, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{Package: "com.example.app", State: "published"}); err != nil {
		t.Fatalf("Run with env fallback: %v", err)
	}
	if !strings.Contains(rt.apiURL, "/appstore/com.env.store/") {
		t.Errorf("url %q should carry the env-supplied app store package name", rt.apiURL)
	}

	if _, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.flag.store", Package: "com.example.app", State: "published"}); err != nil {
		t.Fatalf("Run with flag over env: %v", err)
	}
	if !strings.Contains(rt.apiURL, "/appstore/com.flag.store/") {
		t.Errorf("url %q: the --store-package flag must win over the env var", rt.apiURL)
	}
}

// TestRun_missingPackage_exit2 asserts an unresolvable hosted app package is CLI
// misuse, before any HTTP call.
func TestRun_missingPackage_exit2(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	_, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example.store", State: "published"})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("validation must fail before any HTTP call, got %v", rt.calls)
	}
}

// TestRun_403_namesEnrollment asserts a forbidden flip surfaces as an
// agent-resolvable refusal naming the app store, at the authz exit code.
func TestRun_403_namesEnrollment(t *testing.T) {
	rt := &testRoundTripper{status: 403, resp: `{"error":{"message":"The caller does not have permission"}}`}
	rc := newRC(t, rt)

	_, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.example.store", Package: "com.example.app", State: "unpublished"})
	assertExit(t, err, 11)
	if !strings.Contains(err.Error(), "com.example.store") {
		t.Errorf("refusal %q should name the app store package name", err)
	}
}

// TestRun_404_namesStorePackage asserts an unknown app store (or a hosted app
// record never created) surfaces a hint pointing at --store-package, at the
// API-misuse exit code.
func TestRun_404_namesStorePackage(t *testing.T) {
	rt := &testRoundTripper{status: 404, resp: `{"error":{"message":"not found"}}`}
	rc := newRC(t, rt)

	_, err := publishstatuscmd.Run(rc, publishstatuscmd.Input{StorePackage: "com.unknown.store", Package: "com.example.app", State: "published"})
	assertExit(t, err, 30)
	if !strings.Contains(err.Error(), "store-package") {
		t.Errorf("refusal %q should point at the --store-package flag", err)
	}
}

// TestNewCommand_helpDocumentsTheVerb asserts --help declares the flags, takes
// exactly one positional state, and states the two facts a caller cannot
// discover from the flag names: the state is already PUBLISHED after
// `appstore update`, and the change is reversible in both directions.
func TestNewCommand_helpDocumentsTheVerb(t *testing.T) {
	cmd := publishstatuscmd.NewCommand(kernel.Boot{})
	if !strings.HasPrefix(cmd.Use, "publish-status") {
		t.Errorf("Use = %q, want it to start with publish-status", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short must not be empty")
	}
	for _, flag := range []string{"store-package", "package", "output", "dry-run"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("--%s is not declared", flag)
		}
	}
	// No --confirm gate: unpublishing is reversible by a single call, so
	// ADR-0043's irreversible-AND-external criterion is not met.
	if cmd.Flags().Lookup("confirm") != nil {
		t.Error("--confirm must not be declared: the flip is reversible in both directions")
	}
	if err := cmd.Args(cmd, []string{"published", "extra"}); err == nil {
		t.Error("Args should reject more than one positional state")
	}
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("Args should require the positional state")
	}
	for _, want := range []string{"published", "unpublished", "reversible", "already published", "dry-run"} {
		if !strings.Contains(strings.ToLower(cmd.Long), want) {
			t.Errorf("--help should state %q; got:\n%s", want, cmd.Long)
		}
	}
}

// TestNewCommand_helpUsageShowsBothStates asserts the usage line itself
// enumerates the accepted words, so `--help` answers "what do I type" without
// reading the body.
func TestNewCommand_helpUsageShowsBothStates(t *testing.T) {
	cmd := publishstatuscmd.NewCommand(kernel.Boot{})
	for _, want := range []string{"published", "unpublished"} {
		if !strings.Contains(cmd.Use, want) {
			t.Errorf("Use = %q should enumerate %q", cmd.Use, want)
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
