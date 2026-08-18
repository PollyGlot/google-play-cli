package policy_test

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
	policycmd "github.com/PollyGlot/google-play-cli/commands/appstore/upload/policy"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

const (
	sessionURI  = "https://androidpublisher.googleapis.com/upload/session/abc123"
	defaultResp = `{"fileId":"file-42"}`
)

// testRoundTripper scripts the three legs an upload command performs offline:
// the /token exchange, the resumable initiate POST (answered with a Location
// session URI), and the chunk PUT (answered with the resource body). failWith
// turns the initiate into an API refusal so the exit-code paths are reachable.
// initBody/initBodyCT exist because this is the one upload in the namespace
// whose initiate carries a request body. Nothing here touches the network.
type testRoundTripper struct {
	mu         sync.Mutex
	calls      []string
	initURL    string
	initCT     string // X-Upload-Content-Type announced at initiate
	initBody   []byte
	initBodyCT string // Content-Type of the initiate request itself
	putBody    []byte
	respBody   string
	failWith   int
}

func (r *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)

	switch req.Method {
	case http.MethodPost:
		r.initURL = req.URL.String()
		r.initCT = req.Header.Get("X-Upload-Content-Type")
		r.initBodyCT = req.Header.Get("Content-Type")
		if req.Body != nil {
			r.initBody, _ = io.ReadAll(req.Body)
		}
		if r.failWith != 0 {
			return jsonResp(r.failWith, `{"error":{"message":"nope"}}`), nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Location": []string{sessionURI}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil

	case http.MethodPut:
		if req.Body != nil {
			r.putBody, _ = io.ReadAll(req.Body)
		}
		body := r.respBody
		if body == "" {
			body = defaultResp
		}
		return jsonResp(200, body), nil
	}
	return nil, http.ErrNotSupported
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

// writeDoc drops a stand-in supporting document at a temp path. The leading
// bytes are a real PDF header because the endpoint accepts PDF/JPEG/PNG only
// and the type is sniffed from the file rather than read off the extension.
func writeDoc(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "declaration")
	if err := os.WriteFile(p, []byte("%PDF-1.4\npretend declaration document\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestRun_requestShape asserts the command emits the resumable upload on the
// app store axis: the upload host, the store-keyed path with the hosted app as
// its own segment, uploadType=resumable, the sniffed media type, and no Edit.
func TestRun_requestShape(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	r, err := policycmd.Run(rc, policycmd.Input{StorePackage: "com.example.store", Package: "com.example.app", Path: writeDoc(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.initURL, "/appstore/com.example.store/apps/com.example.app/policyDeclarationFiles:upload") {
		t.Errorf("url %q is not the appstoreappsreview.uploadAppStoreAppPolicyDeclarationFile endpoint", rt.initURL)
	}
	if !strings.Contains(rt.initURL, "uploadType=resumable") {
		t.Errorf("url %q does not request a resumable upload", rt.initURL)
	}
	if strings.Contains(rt.initURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", rt.initURL)
	}
	if rt.initCT != "application/pdf" {
		t.Errorf("X-Upload-Content-Type = %q, want application/pdf sniffed from the file's leading bytes", rt.initCT)
	}
	if !strings.Contains(string(rt.putBody), "pretend declaration document") {
		t.Errorf("chunk PUT did not carry the file bytes: %q", rt.putBody)
	}
	if r == nil {
		t.Fatal("Run returned a nil payload")
	}
}

// TestRun_initiateCarriesFileType asserts what makes this upload unlike its two
// siblings: the method takes a request body, and `fileType` is required. The
// caller never chooses it — the enum's only other member is the UNSPECIFIED
// zero value — so the JSON must travel whether or not a flag was passed, with
// the initiate's own Content-Type distinct from the media type above.
func TestRun_initiateCarriesFileType(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	if _, err := policycmd.Run(rc, policycmd.Input{StorePackage: "com.example.store", Package: "com.example.app", Path: writeDoc(t)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.initBodyCT, "application/json") {
		t.Errorf("initiate Content-Type = %q, want application/json", rt.initBodyCT)
	}
	var body struct {
		FileType string `json:"fileType"`
	}
	if err := json.Unmarshal(rt.initBody, &body); err != nil {
		t.Fatalf("initiate body %q is not JSON: %v", rt.initBody, err)
	}
	if body.FileType != appstore.DeclarationFileTypeDocument {
		t.Errorf("initiate fileType = %q, want %q", body.FileType, appstore.DeclarationFileTypeDocument)
	}
}

// TestRun_rendersTrackingID asserts the human views lead with the fileId and
// say where it has to be cited — inside the policy response's documentResponse.
// An id with no destination is a dead end. The views also echo the fileType the
// caller never chose, so nothing travelled on the wire unseen.
func TestRun_rendersTrackingID(t *testing.T) {
	rt := &testRoundTripper{respBody: `{"fileId":"file-42"}`}
	rc := newRC(t, rt)

	r, err := policycmd.Run(rc, policycmd.Input{StorePackage: "com.example.store", Package: "com.example.app", Path: writeDoc(t)})
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
		for _, want := range []string{"file-42", "com.example.store", "com.example.app", "appstore update", "documentResponse", appstore.DeclarationFileTypeDocument} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s view %q missing %q", name, out.String(), want)
			}
		}
	}
}

// TestRun_jsonPassthrough asserts --output json emits the API response verbatim
// (ADR-0003), including fields the response schema does not model.
func TestRun_jsonPassthrough(t *testing.T) {
	const body = `{"fileId":"file-42","unmodeledFutureField":"kept"}`
	rt := &testRoundTripper{respBody: body}
	rc := newRC(t, rt)

	r, err := policycmd.Run(rc, policycmd.Input{StorePackage: "com.example.store", Package: "com.example.app", Path: writeDoc(t)})
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

// TestPayload_jsonEmptyBodyFallback asserts a bodiless success still yields
// parseable JSON — the documented ADR-0003 exception, never zero bytes on the
// CI default format.
func TestPayload_jsonEmptyBodyFallback(t *testing.T) {
	p := policycmd.Payload{StorePackage: "com.example.store", Package: "com.example.app", FileID: "file-42"}
	var out bytes.Buffer
	if err := p.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got struct {
		OK     bool   `json:"ok"`
		FileID string `json:"fileId"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json %q is not parseable: %v", out.String(), err)
	}
	if !got.OK || got.FileID != "file-42" {
		t.Errorf("fallback json = %+v", got)
	}
}

// TestRun_packageDefaultsToProjectPin asserts an omitted --package falls back to
// the repo pin, like every other package-axis command.
func TestRun_packageDefaultsToProjectPin(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)
	rc.Resolved = &config.Resolved{Pin: "com.pinned.app"}

	if _, err := policycmd.Run(rc, policycmd.Input{StorePackage: "com.example.store", Path: writeDoc(t)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.initURL, "/apps/com.pinned.app/") {
		t.Errorf("url %q should carry the project pin", rt.initURL)
	}
}

// TestRun_storePackageFallsBackToEnv asserts the app store axis reads the
// ADR-0043 cascade: an omitted --store-package takes $GPLAY_APP_STORE_PACKAGE,
// so a CI job exports it once for the whole namespace. The project pin never
// feeds this axis — it pins a package, never a store.
func TestRun_storePackageFallsBackToEnv(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "com.env.store")
	rt := &testRoundTripper{}
	rc := newRC(t, rt)
	rc.Resolved = &config.Resolved{Pin: "com.pinned.app"}

	if _, err := policycmd.Run(rc, policycmd.Input{Package: "com.example.app", Path: writeDoc(t)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.initURL, "/appstore/com.env.store/") {
		t.Errorf("url %q should carry the app store package name from the environment", rt.initURL)
	}
}

// TestRun_dryRun_noHTTPNoFileRead asserts --dry-run resolves the target,
// performs no HTTP call at all (not even the token exchange) and never opens
// the file — the path deliberately does not exist, so a job can rehearse an
// upload while the document is still being assembled. The rehearsal also names
// the fileType, since the real call would send it unasked.
func TestRun_dryRun_noHTTPNoFileRead(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)
	missing := filepath.Join(t.TempDir(), "not-signed-yet.pdf")

	r, err := policycmd.Run(rc, policycmd.Input{StorePackage: "com.example.store", Package: "com.example.app", Path: missing, DryRun: true})
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
	for _, want := range []string{"would upload", "(dry-run)", "com.example.store", "com.example.app"} {
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
		File                string    `json:"file"`
		FileType            string    `json:"fileType"`
		Requires            *[]string `json:"requires"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json %q is not parseable: %v", out.String(), err)
	}
	if !got.DryRun || got.AppStorePackageName != "com.example.store" || got.PackageName != "com.example.app" || got.File != missing {
		t.Errorf("dry-run json = %+v", got)
	}
	if got.FileType != appstore.DeclarationFileTypeDocument {
		t.Errorf("dry-run json fileType = %q, want %q — the body field the caller never sets", got.FileType, appstore.DeclarationFileTypeDocument)
	}
	if got.Requires == nil || len(*got.Requires) != 0 {
		t.Errorf("dry-run json %q must carry an empty requires array, got %v", out.String(), got.Requires)
	}
}

// TestRun_missingStorePackage_exit2 asserts the app store package name is
// required and its absence is CLI misuse — it identifies the caller and has no
// project-level default (the Project pin pins a package, never a store).
func TestRun_missingStorePackage_exit2(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	_, err := policycmd.Run(rc, policycmd.Input{Package: "com.example.app", Path: writeDoc(t)})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "store-package") {
		t.Errorf("error %q should name the --store-package flag", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("validation must fail before any HTTP call, got %v", rt.calls)
	}
}

// TestRun_missingFile_exit20 keeps an unreadable local path a client-side
// validation failure rather than a transport one (exit 50).
func TestRun_missingFile_exit20(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	_, err := policycmd.Run(rc, policycmd.Input{StorePackage: "com.example.store", Package: "com.example.app", Path: filepath.Join(t.TempDir(), "nope.pdf")})
	assertExit(t, err, 20)
	if rt.initURL != "" {
		t.Errorf("a missing file must fail before the upload starts, got %q", rt.initURL)
	}
}

// TestRun_directory_exit20 covers the path that opens and stats fine but cannot
// be streamed: without the regular-file guard its size would be meaningless and
// the failure would surface as transport (exit 50) mid-transfer.
func TestRun_directory_exit20(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	_, err := policycmd.Run(rc, policycmd.Input{StorePackage: "com.example.store", Package: "com.example.app", Path: t.TempDir()})
	assertExit(t, err, 20)
	if rt.initURL != "" {
		t.Errorf("a directory must fail before the upload starts, got %q", rt.initURL)
	}
}

// TestRun_403_namesEnrollment asserts a forbidden upload surfaces as an
// agent-resolvable refusal naming the app store, at the authz exit code.
func TestRun_403_namesEnrollment(t *testing.T) {
	rt := &testRoundTripper{failWith: http.StatusForbidden}
	rc := newRC(t, rt)

	_, err := policycmd.Run(rc, policycmd.Input{StorePackage: "com.example.store", Package: "com.example.app", Path: writeDoc(t)})
	assertExit(t, err, 11)
	if !strings.Contains(err.Error(), "com.example.store") {
		t.Errorf("refusal %q should name the app store package name", err)
	}
}

// TestRun_404_namesStorePackage asserts an unknown app store or hosted app
// surfaces a hint pointing at --store-package, at the API-misuse exit code.
func TestRun_404_namesStorePackage(t *testing.T) {
	rt := &testRoundTripper{failWith: http.StatusNotFound}
	rc := newRC(t, rt)

	_, err := policycmd.Run(rc, policycmd.Input{StorePackage: "com.unknown.store", Package: "com.example.app", Path: writeDoc(t)})
	assertExit(t, err, 30)
	if !strings.Contains(err.Error(), "store-package") {
		t.Errorf("refusal %q should point at the --store-package flag", err)
	}
}

// TestNewCommand_helpDocumentsTheVerb asserts --help explains the verb, the
// role of the id, and the API's size/type constraint, and declares the flags.
func TestNewCommand_helpDocumentsTheVerb(t *testing.T) {
	cmd := policycmd.NewCommand(kernel.Boot{})
	if cmd.Use != "policy <file>" {
		t.Errorf("Use = %q, want policy <file>", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short must not be empty")
	}
	for _, flag := range []string{"store-package", "package", "output", "dry-run"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("--%s is not declared", flag)
		}
	}
	// An upload is inert, so it fails the ADR-0043 gate criterion: the flag must
	// not exist at all rather than exist and be optional.
	if cmd.Flags().Lookup("confirm") != nil {
		t.Error("--confirm must not be declared: an upload is not irreversible AND externally visible (ADR-0043)")
	}
	// The facts a caller cannot discover from the flag names: what the id is
	// for, that nothing is declared until `appstore update`, the size limit, the
	// fileType sent unasked, and that no confirmation is required.
	for _, want := range []string{"fileid", "documentresponse", "10 mib", strings.ToLower(appstore.DeclarationFileTypeDocument), "no --confirm", "nothing is declared"} {
		if !strings.Contains(strings.ToLower(cmd.Long), want) {
			t.Errorf("--help should state %q; got:\n%s", want, cmd.Long)
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
