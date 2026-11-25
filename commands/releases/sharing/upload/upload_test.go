package upload_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	uploadcmd "github.com/PollyGlot/google-play-cli/commands/releases/sharing/upload"
	"github.com/PollyGlot/google-play-cli/internal/artifacttest"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const artifactBody = `{"downloadUrl":"https://play.google.com/apps/test/abc123","certificateFingerprint":"AA:BB:CC","sha256":"deadbeef"}`

func newFake() *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		return http.StatusOK, artifactBody, c.Method == http.MethodPost && strings.Contains(c.Path, "/internalappsharing/")
	})
}

func uploadURL(fake *testkit.Fake) string {
	for _, c := range fake.Calls() {
		if c.Method == http.MethodPost && strings.Contains(c.Path, "/internalappsharing/") {
			return c.URL
		}
	}
	return ""
}

func networkCalls(fake *testkit.Fake) int { return len(fake.Calls()) + fake.TokenExchanges() }

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key := testkit.RSAKey(t)
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

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, &stdout
}

func writeArtifact(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if strings.HasSuffix(name, ".apk") {
		return artifacttest.APK(t, dir, name, "com.example.app")
	}
	return artifacttest.AAB(t, dir, name, "com.example.app")
}

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err = %v (%T), want an ExitCode()", err, err)
	}
	return c.ExitCode()
}

func TestRun_apk_happyPath(t *testing.T) {
	fake := newFake()
	rc, _ := newRC(t, fake)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: writeArtifact(t, "app.apk")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fake.TokenExchanges() == 0 {
		t.Errorf("no /token exchange; calls=%v", fake.Calls())
	}
	for _, want := range []string{"/applications/internalappsharing/com.example.app/artifacts/apk", "uploadType=media"} {
		if !strings.Contains(uploadURL(fake), want) {
			t.Errorf("upload url %q missing %q", uploadURL(fake), want)
		}
	}
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if strings.TrimSpace(jsonOut.String()) != artifactBody {
		t.Errorf("json = %s, want verbatim artifact pass-through", jsonOut.String())
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") || !strings.Contains(stderr.String(), "abc123") {
		t.Errorf("stderr missing ✓ line with the link:\n%s", stderr.String())
	}
}

func TestRun_aab_usesBundleEndpoint(t *testing.T) {
	fake := newFake()
	rc, _ := newRC(t, fake)
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: writeArtifact(t, "app.aab")}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(uploadURL(fake), "/artifacts/bundle") {
		t.Errorf("upload url %q should hit the bundle endpoint", uploadURL(fake))
	}
}

func TestRun_formatOverride(t *testing.T) {
	fake := newFake()
	rc, _ := newRC(t, fake)
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: writeArtifact(t, "build.bin"), Format: "bundle"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(uploadURL(fake), "/artifacts/bundle") {
		t.Errorf("upload url %q should honor --format bundle", uploadURL(fake))
	}
}

func TestRun_unknownExtension_exit2_noNetwork(t *testing.T) {
	fake := newFake()
	rc, _ := newRC(t, fake)
	_, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: writeArtifact(t, "build.bin")})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if err == nil || !strings.Contains(err.Error(), "pass --format apk|bundle") {
		t.Errorf("err = %v, want it to name --format apk|bundle", err)
	}
	if networkCalls(fake) != 0 {
		t.Errorf("unknown extension must make no network call; calls=%v", fake.Calls())
	}
}

func TestRun_directory_exit20_noNetwork(t *testing.T) {
	fake := newFake()
	rc, _ := newRC(t, fake)
	_, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: t.TempDir(), Format: "apk"})
	if got := exitOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20; err=%v", got, err)
	}
	if networkCalls(fake) != 0 {
		t.Errorf("a directory must make no network call; calls=%v", fake.Calls())
	}
}

func TestRun_missingPackage_exit2_noNetwork(t *testing.T) {
	fake := newFake()
	rc, _ := newRC(t, fake)
	_, err := uploadcmd.Run(rc, uploadcmd.Input{ArtifactPath: writeArtifact(t, "app.apk")})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if networkCalls(fake) != 0 {
		t.Errorf("missing package must make no network call; calls=%v", fake.Calls())
	}
}

func TestRun_dryRun_noNetwork_noConfirmation(t *testing.T) {
	fake := newFake()
	rc, _ := newRC(t, fake)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: writeArtifact(t, "app.apk"), DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if networkCalls(fake) != 0 {
		t.Errorf("dry-run must make no network call; calls=%v", fake.Calls())
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓; stderr=%q", stderr.String())
	}
	var view struct {
		DryRun   bool      `json:"dryRun"`
		Package  string    `json:"package"`
		Format   string    `json:"format"`
		Requires *[]string `json:"requires"`
	}
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &view); err != nil {
		t.Fatalf("dry-run json not parseable: %v", err)
	}
	if !view.DryRun || view.Package != "com.example.app" || view.Format != "apk" {
		t.Errorf("dry-run view = %+v, want dryRun apk for com.example.app", view)
	}
	if view.Requires == nil || len(*view.Requires) != 0 {
		t.Errorf("dry-run json = %s, want \"requires\": []", jsonOut.String())
	}
}

func TestNewCommand_flags(t *testing.T) {
	cmd := uploadcmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"package", "format", "dry-run", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
}
