package enroll_test

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

	enrollcmd "github.com/PollyGlot/google-play-cli/commands/signing/enroll"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const enrollBody = `{"signingCertificate":{"certificateHashMd5":"AA:BB","certificateHashSha1":"CC:DD","certificateHashSha256":"EE:FF"},"uploadCertificate":{"certificateHashSha256":"11:22"}}`

// rt is the offline transport: it answers the OAuth token exchange and the
// single appsigning POST, recording the request for shape assertions.
type rt struct {
	mu     sync.Mutex
	calls  []string
	url    string
	method string
	body   []byte
	resp   string
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(`{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.url = req.URL.String()
	r.method = req.Method
	if req.Body != nil {
		r.body, _ = io.ReadAll(req.Body)
	}
	body := r.resp
	if body == "" {
		body = enrollBody
	}
	return jsonResp(body), nil
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

func newRC(t *testing.T, transport http.RoundTripper, format output.Format) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: transport})
	var stdout bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout}, kernel.Inputs{Format: format})
	rc.Account = sa
	return rc, &stdout
}

// writeFile drops content in a temp dir and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

const certPEM = "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n"

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v carries no exit code", err)
	}
	return c.ExitCode()
}

// render runs the payload through the requested format, as the kernel would.
func render(t *testing.T, r output.Renderable, format output.Format) string {
	t.Helper()
	var buf bytes.Buffer
	if err := output.Render(&buf, format, r.Renderers()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

// TestRun_missingConfirm_exit3_noNetwork asserts the irreversible-write gate:
// without --confirm the command refuses (exit 3) before touching the network.
func TestRun_missingConfirm_exit3_noNetwork(t *testing.T) {
	r := &rt{}
	rc, _ := newRC(t, r, output.FormatJSON)
	_, err := enrollcmd.Run(rc, enrollcmd.Input{Package: "com.example.app", KmsKey: "projects/p/cryptoKeyVersions/1"})
	if got := exitCode(t, err); got != 3 {
		t.Errorf("exit = %d, want 3", got)
	}
	if !strings.Contains(err.Error(), "--confirm") {
		t.Errorf("error must name the flag: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("must not reach the network without --confirm; calls=%v", r.calls)
	}
}

// TestRun_dryRun_previewsWithoutHTTP asserts --dry-run rehearses offline and
// reports the gate through the ADR-0017 `requires` array.
func TestRun_dryRun_previewsWithoutHTTP(t *testing.T) {
	r := &rt{}
	rc, _ := newRC(t, r, output.FormatJSON)
	got, err := enrollcmd.Run(rc, enrollcmd.Input{Package: "com.example.app", KmsKey: "k", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("--dry-run must not reach the network; calls=%v", r.calls)
	}
	out := render(t, got, output.FormatJSON)
	for _, want := range []string{`"dryRun": true`, `"action": "enroll"`, `"confirm"`} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run json missing %q:\n%s", want, out)
		}
	}
}

// TestRun_existingApp_postsEnrollExistingApp asserts the oneof branch, the URL
// and the verbatim JSON pass-through (ADR-0003).
func TestRun_existingApp_postsEnrollExistingApp(t *testing.T) {
	r := &rt{}
	rc, _ := newRC(t, r, output.FormatJSON)
	got, err := enrollcmd.Run(rc, enrollcmd.Input{Package: "com.example.app", KmsKey: "projects/p/cryptoKeyVersions/1", Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.method != http.MethodPost {
		t.Errorf("method = %q, want POST", r.method)
	}
	if !strings.HasSuffix(r.url, "/applications/com.example.app/appSigning:enrollApp") {
		t.Errorf("url %q should hit appSigning:enrollApp", r.url)
	}
	var body map[string]any
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if _, ok := body["enrollNewApp"]; ok {
		t.Errorf("existing-app enroll must not set enrollNewApp: %s", r.body)
	}
	existing, ok := body["enrollExistingApp"].(map[string]any)
	if !ok {
		t.Fatalf("missing enrollExistingApp: %s", r.body)
	}
	key, _ := existing["cloudKmsKey"].(map[string]any)
	if key["cryptoKeyVersionResource"] != "projects/p/cryptoKeyVersions/1" {
		t.Errorf("cloudKmsKey = %v, want the --kms-key resource", existing["cloudKmsKey"])
	}
	if out := render(t, got, output.FormatJSON); strings.TrimSpace(out) != enrollBody {
		t.Errorf("--output json must mirror the API response verbatim (ADR-0003):\n%s", out)
	}
}

// TestRun_newApp_postsEnrollNewApp_base64Cert asserts the new-app branch carries
// the KMS key AND the base64 of the PEM file's bytes.
func TestRun_newApp_postsEnrollNewApp_base64Cert(t *testing.T) {
	r := &rt{}
	rc, _ := newRC(t, r, output.FormatJSON)
	certPath := writeFile(t, "kms.pem", certPEM)
	uploadPath := writeFile(t, "upload.pem", certPEM+"upload\n")
	_, err := enrollcmd.Run(rc, enrollcmd.Input{
		Package: "com.example.app", KmsKey: "k", NewApp: true,
		KmsCert: certPath, UploadCert: uploadPath, Confirm: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var body struct {
		EnrollNewApp *struct {
			CloudKmsKeyAndCert struct {
				CloudKmsKey    struct{ CryptoKeyVersionResource string }
				PemCertificate []byte
			}
		}
		EnrollExistingApp    *json.RawMessage
		PemUploadCertificate []byte
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if body.EnrollExistingApp != nil {
		t.Errorf("--new-app must not set enrollExistingApp: %s", r.body)
	}
	if body.EnrollNewApp == nil {
		t.Fatalf("--new-app must set enrollNewApp: %s", r.body)
	}
	if got := string(body.EnrollNewApp.CloudKmsKeyAndCert.PemCertificate); got != certPEM {
		t.Errorf("pemCertificate decodes to %q, want the file's bytes", got)
	}
	if got := string(body.PemUploadCertificate); got != certPEM+"upload\n" {
		t.Errorf("pemUploadCertificate decodes to %q, want the upload file's bytes", got)
	}
}

// TestRun_flagCombinations_areUsageErrors asserts the oneof distinction is
// enforced client-side (exit 2), never sent to the API as a wrong shape.
func TestRun_flagCombinations_areUsageErrors(t *testing.T) {
	certPath := writeFile(t, "kms.pem", certPEM)
	cases := []struct {
		name string
		in   enrollcmd.Input
		want string
	}{
		{"new-app without cert", enrollcmd.Input{Package: "p", KmsKey: "k", NewApp: true, Confirm: true}, "--kms-cert"},
		{"cert without new-app", enrollcmd.Input{Package: "p", KmsKey: "k", KmsCert: certPath, Confirm: true}, "--new-app"},
		{"no kms key", enrollcmd.Input{Package: "p", Confirm: true}, "--kms-key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &rt{}
			rc, _ := newRC(t, r, output.FormatJSON)
			_, err := enrollcmd.Run(rc, tc.in)
			if got := exitCode(t, err); got != 2 {
				t.Errorf("exit = %d, want 2 (%v)", got, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v should name %s", err, tc.want)
			}
			if len(r.calls) != 0 {
				t.Errorf("a client-side error must not reach the network; calls=%v", r.calls)
			}
		})
	}
}

// TestRun_nonPEMCert_isUsageError asserts a file that is not a certificate is
// caught locally rather than as an opaque 400.
func TestRun_nonPEMCert_isUsageError(t *testing.T) {
	r := &rt{}
	rc, _ := newRC(t, r, output.FormatJSON)
	bad := writeFile(t, "cert.der", "not a pem file")
	_, err := enrollcmd.Run(rc, enrollcmd.Input{Package: "p", KmsKey: "k", NewApp: true, KmsCert: bad, Confirm: true})
	if got := exitCode(t, err); got != 2 {
		t.Errorf("exit = %d, want 2 (%v)", got, err)
	}
	if len(r.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", r.calls)
	}
}

// TestRun_tablePrintsCertificateHashes asserts the human view surfaces the
// returned hashes, one row per certificate the API actually sent.
func TestRun_tablePrintsCertificateHashes(t *testing.T) {
	r := &rt{}
	rc, _ := newRC(t, r, output.FormatTable)
	got, err := enrollcmd.Run(rc, enrollcmd.Input{Package: "com.example.app", KmsKey: "k", Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := render(t, got, output.FormatTable)
	for _, want := range []string{"CERTIFICATE", "SHA256", "signing", "EE:FF", "upload", "11:22"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

// TestRun_noUploadCertificate_omitsTheRow asserts gplay never fabricates a hash
// the API did not return.
func TestRun_noUploadCertificate_omitsTheRow(t *testing.T) {
	r := &rt{resp: `{"signingCertificate":{"certificateHashSha256":"EE:FF"}}`}
	rc, _ := newRC(t, r, output.FormatTable)
	got, err := enrollcmd.Run(rc, enrollcmd.Input{Package: "com.example.app", KmsKey: "k", Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out := render(t, got, output.FormatTable); strings.Contains(out, "upload") {
		t.Errorf("no uploadCertificate in the response, so no upload row:\n%s", out)
	}
}

// TestNewCommand_helpNamesTheApiOnlyBoundary asserts --help states the trap: the
// standard, Google-managed enrollment is not reachable through any API.
func TestNewCommand_helpNamesTheApiOnlyBoundary(t *testing.T) {
	long := enrollcmd.NewCommand(kernel.Boot{}).Long
	for _, want := range []string{
		"NOT standard Play App Signing enrollment",
		"CANNOT be done through the API",
		"Cloud KMS",
		"https://support.google.com/googleplay/android-developer/answer/9842756",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("--help must mention %q:\n%s", want, long)
		}
	}
}
