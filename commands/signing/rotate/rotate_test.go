package rotate_test

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

	rotatecmd "github.com/PollyGlot/google-play-cli/commands/signing/rotate"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const rotateBody = `{"rotatedKeyCertificate":{"certificateHashMd5":"AA:BB","certificateHashSha1":"CC:DD","certificateHashSha256":"EE:FF"}}`

const (
	certPEM    = "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n"
	lineageBin = "\x00\x01lineage-bytes\xff"
)

// rt is the offline transport: it answers the OAuth token exchange and the
// single appsigning POST, recording the request for shape assertions.
type rt struct {
	mu     sync.Mutex
	calls  []string
	url    string
	method string
	body   []byte
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
	return jsonResp(rotateBody), nil
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

func newRC(t *testing.T, transport http.RoundTripper, format output.Format) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: transport})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: format})
	rc.Account = sa
	return rc
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// validInput returns a complete, valid --confirm-ed input; each test mutates the
// one field it is about.
func validInput(t *testing.T) rotatecmd.Input {
	t.Helper()
	dir := t.TempDir()
	return rotatecmd.Input{
		Package: "com.example.app",
		KmsKey:  "projects/p/cryptoKeyVersions/2",
		KmsCert: writeFile(t, dir, "new.pem", certPEM),
		Lineage: writeFile(t, dir, "lineage.bin", lineageBin),
		Reason:  "routine-key-upgrade",
		Confirm: true,
	}
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v carries no exit code", err)
	}
	return c.ExitCode()
}

func render(t *testing.T, r output.Renderable, format output.Format) string {
	t.Helper()
	var buf bytes.Buffer
	if err := output.Render(&buf, format, r.Renderers()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

// TestRun_missingConfirm_exit3_noNetwork asserts the irreversible-write gate.
func TestRun_missingConfirm_exit3_noNetwork(t *testing.T) {
	r := &rt{}
	in := validInput(t)
	in.Confirm = false
	_, err := rotatecmd.Run(newRC(t, r, output.FormatJSON), in)
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

// TestRun_dryRun_previewsWithoutHTTP asserts --dry-run rehearses offline.
func TestRun_dryRun_previewsWithoutHTTP(t *testing.T) {
	r := &rt{}
	in := validInput(t)
	in.Confirm = false
	in.DryRun = true
	got, err := rotatecmd.Run(newRC(t, r, output.FormatJSON), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("--dry-run must not reach the network; calls=%v", r.calls)
	}
	out := render(t, got, output.FormatJSON)
	for _, want := range []string{`"dryRun": true`, `"action": "rotate"`, `"confirm"`} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run json missing %q:\n%s", want, out)
		}
	}
}

// TestRun_postsRotateRequest asserts the URL, the nesting, the enum mapping and
// the base64 of BOTH byte fields (certificate and lineage).
func TestRun_postsRotateRequest(t *testing.T) {
	r := &rt{}
	in := validInput(t)
	in.Reason = "compromised-key"
	got, err := rotatecmd.Run(newRC(t, r, output.FormatJSON), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.method != http.MethodPost {
		t.Errorf("method = %q, want POST", r.method)
	}
	if !strings.HasSuffix(r.url, "/applications/com.example.app/appSigning:rotateAppSigningKey") {
		t.Errorf("url %q should hit appSigning:rotateAppSigningKey", r.url)
	}
	var body struct {
		RotatedCloudKmsKey struct {
			CloudKmsKeyAndCert struct {
				CloudKmsKey    struct{ CryptoKeyVersionResource string }
				PemCertificate []byte
			}
			SigningCertificateLineage []byte
		}
		KeyRotationReason string
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if got := body.RotatedCloudKmsKey.CloudKmsKeyAndCert.CloudKmsKey.CryptoKeyVersionResource; got != in.KmsKey {
		t.Errorf("cryptoKeyVersionResource = %q, want %q", got, in.KmsKey)
	}
	if got := string(body.RotatedCloudKmsKey.CloudKmsKeyAndCert.PemCertificate); got != certPEM {
		t.Errorf("pemCertificate decodes to %q, want the PEM file's bytes", got)
	}
	if got := string(body.RotatedCloudKmsKey.SigningCertificateLineage); got != lineageBin {
		t.Errorf("signingCertificateLineage decodes to %q, want the lineage file's bytes", got)
	}
	if body.KeyRotationReason != "COMPROMISED_KEY" {
		t.Errorf("keyRotationReason = %q, want COMPROMISED_KEY", body.KeyRotationReason)
	}
	if out := render(t, got, output.FormatJSON); strings.TrimSpace(out) != rotateBody {
		t.Errorf("--output json must mirror the API response verbatim (ADR-0003):\n%s", out)
	}
}

// TestRun_everyReasonMapsToTheApiEnum pins the whole --reason vocabulary.
func TestRun_everyReasonMapsToTheApiEnum(t *testing.T) {
	want := map[string]string{
		"compromised-key":                "COMPROMISED_KEY",
		"use-stronger-key":               "USE_STRONGER_KEY",
		"use-same-key-for-multiple-apps": "USE_SAME_KEY_FOR_MULTIPLE_APPS",
		"routine-key-upgrade":            "ROUTINE_KEY_UPGRADE",
		"other":                          "OTHER",
	}
	for choice, enum := range want {
		t.Run(choice, func(t *testing.T) {
			r := &rt{}
			in := validInput(t)
			in.Reason = choice
			if _, err := rotatecmd.Run(newRC(t, r, output.FormatJSON), in); err != nil {
				t.Fatalf("Run: %v", err)
			}
			var body struct{ KeyRotationReason string }
			if err := json.Unmarshal(r.body, &body); err != nil {
				t.Fatalf("request body is not JSON: %v", err)
			}
			if body.KeyRotationReason != enum {
				t.Errorf("keyRotationReason = %q, want %q", body.KeyRotationReason, enum)
			}
		})
	}
}

// TestRun_missingOrInvalidFlags_areUsageErrors asserts every required flag and
// the --reason vocabulary are enforced client-side (exit 2), offline.
func TestRun_missingOrInvalidFlags_areUsageErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*rotatecmd.Input)
		wantMsg string
	}{
		{"no kms key", func(in *rotatecmd.Input) { in.KmsKey = "" }, "--kms-key"},
		{"no kms cert", func(in *rotatecmd.Input) { in.KmsCert = "" }, "--kms-cert"},
		{"no lineage", func(in *rotatecmd.Input) { in.Lineage = "" }, "--lineage"},
		{"no reason", func(in *rotatecmd.Input) { in.Reason = "" }, "--reason"},
		{"unknown reason", func(in *rotatecmd.Input) { in.Reason = "because" }, "routine-key-upgrade"},
		{"lineage file missing", func(in *rotatecmd.Input) { in.Lineage = filepath.Join(t.TempDir(), "nope.bin") }, "--lineage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &rt{}
			in := validInput(t)
			tc.mutate(&in)
			_, err := rotatecmd.Run(newRC(t, r, output.FormatJSON), in)
			if got := exitCode(t, err); got != 2 {
				t.Errorf("exit = %d, want 2 (%v)", got, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %v should mention %q", err, tc.wantMsg)
			}
			if len(r.calls) != 0 {
				t.Errorf("a client-side error must not reach the network; calls=%v", r.calls)
			}
		})
	}
}

// TestRun_tablePrintsRotatedKeyHashes asserts the human view surfaces the
// rotated key's hashes.
func TestRun_tablePrintsRotatedKeyHashes(t *testing.T) {
	r := &rt{}
	got, err := rotatecmd.Run(newRC(t, r, output.FormatTable), validInput(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := render(t, got, output.FormatTable)
	for _, want := range []string{"CERTIFICATE", "rotatedKey", "EE:FF", "CC:DD", "AA:BB"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

// TestNewCommand_helpNamesTheKmsOnlyBoundary asserts --help states the trap:
// rotation via API applies only to self-hosted Cloud KMS keys.
func TestNewCommand_helpNamesTheKmsOnlyBoundary(t *testing.T) {
	cmd := rotatecmd.NewCommand(kernel.Boot{})
	for _, want := range []string{
		"ONLY to apps enrolled with a self-hosted Cloud KMS key",
		"Google Play\nConsole UI",
		"apksigner",
		"https://support.google.com/googleplay/android-developer/answer/9842756",
		"routine-key-upgrade",
	} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("--help must mention %q:\n%s", want, cmd.Long)
		}
	}
}
