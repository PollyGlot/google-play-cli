package doctor_test

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
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

// roundTripperFunc — canonical AGENTS.md pattern.
type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newOpts(t *testing.T) doctor.Options {
	t.Helper()
	root := t.TempDir()
	return doctor.Options{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
	}
}

// signedSAJSON produces a service-account JSON whose private_key is a
// freshly generated RSA key so the OAuth2 library can sign the
// exchange JWT in tests.
func signedSAJSON(t *testing.T) []byte {
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

func seedActiveAccount(t *testing.T, opts doctor.Options, saBytes []byte) {
	t.Helper()
	be := keystore.NewFileBackend(opts.KeystoreRoot)
	if err := be.Save("playci", saBytes); err != nil {
		t.Fatalf("keystore.Save: %v", err)
	}
	cfg := &config.Config{}
	cfg.AddAccount("playci")
	if err := cfg.SetActive("playci"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := cfg.Save(opts.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}
}

func runCmd(t *testing.T, opts doctor.Options, ctx context.Context, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	cmd := doctor.NewCommand(opts)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	cmd.SetContext(ctx)
	return cmd.Execute()
}

// successRT returns a roundTripperFunc that responds with a healthy
// OAuth2 token payload so checks 2 and 3 pass.
func successRT() roundTripperFunc {
	return func(req *http.Request) (*http.Response, error) {
		body := `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	}
}

func ctxWithRT(fn roundTripperFunc) context.Context {
	return context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: fn})
}

func TestDoctor_happyPath_prints3CheckmarksAndExits0(t *testing.T) {
	opts := newOpts(t)
	seedActiveAccount(t, opts, signedSAJSON(t))

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, opts, ctxWithRT(successRT()), &stdout, &stderr); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	combined := stdout.String() + stderr.String()
	checks := strings.Count(combined, "✅")
	if checks != 3 {
		t.Errorf("checkmark count = %d, want 3; combined output:\n%s", checks, combined)
	}
	if strings.Contains(combined, "❌") {
		t.Errorf("combined output contains ❌ on happy path:\n%s", combined)
	}
}

func TestDoctor_malformedSA_failsCheck1_skipsRest(t *testing.T) {
	opts := newOpts(t)
	bad := []byte(`{"type":"service_account","client_email":"","private_key":"","token_uri":""}`)
	// We seed with bytes that the keystore accepts but the doctor will
	// detect as missing required fields at resolution time.
	be := keystore.NewFileBackend(opts.KeystoreRoot)
	if err := be.Save("playci", bad); err != nil {
		t.Fatalf("keystore.Save: %v", err)
	}
	cfg := &config.Config{}
	cfg.AddAccount("playci")
	if err := cfg.SetActive("playci"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := cfg.Save(opts.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := runCmd(t, opts, ctxWithRT(successRT()), &stdout, &stderr)
	if err == nil {
		t.Fatal("Execute: expected error on malformed SA, got nil")
	}
	if got := doctor.ExitCode(err); got != 10 {
		t.Errorf("ExitCode(err) = %d, want 10", got)
	}
}

func TestDoctor_jsonOutput_passesThroughCheckResults(t *testing.T) {
	opts := newOpts(t)
	seedActiveAccount(t, opts, signedSAJSON(t))

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, opts, ctxWithRT(successRT()), &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var parsed []struct {
		Name     string `json:"name"`
		Passed   bool   `json:"passed"`
		Skipped  bool   `json:"skipped"`
		ExitCode int    `json:"exit_code"`
		Hint     string `json:"hint,omitempty"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, stdout.String())
	}
	if len(parsed) != 3 {
		t.Fatalf("len(parsed) = %d, want 3", len(parsed))
	}
	for i, r := range parsed {
		if !r.Passed {
			t.Errorf("result[%d].Passed = false on happy path (%+v)", i, r)
		}
		if r.Skipped {
			t.Errorf("result[%d].Skipped = true on happy path (%+v)", i, r)
		}
		if r.Name == "" {
			t.Errorf("result[%d].Name is empty", i)
		}
	}
}

func TestDoctor_jsonOutput_failingCheck_includesSkippedRest(t *testing.T) {
	opts := newOpts(t)
	bad := []byte(`{"type":"service_account","client_email":"","private_key":"","token_uri":""}`)
	be := keystore.NewFileBackend(opts.KeystoreRoot)
	if err := be.Save("playci", bad); err != nil {
		t.Fatalf("keystore.Save: %v", err)
	}
	cfg := &config.Config{}
	cfg.AddAccount("playci")
	if err := cfg.SetActive("playci"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := cfg.Save(opts.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := runCmd(t, opts, ctxWithRT(successRT()), &stdout, &stderr, "--output", "json")
	if err == nil {
		t.Fatal("Execute: expected error on malformed SA, got nil")
	}
	if got := doctor.ExitCode(err); got != 10 {
		t.Errorf("ExitCode(err) = %d, want 10", got)
	}

	var parsed []struct {
		Name     string `json:"name"`
		Passed   bool   `json:"passed"`
		Skipped  bool   `json:"skipped"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, stdout.String())
	}
	if len(parsed) != 3 {
		t.Fatalf("len(parsed) = %d, want 3", len(parsed))
	}
	if parsed[0].Passed || parsed[0].Skipped {
		t.Errorf("result[0] = %+v, want first check failed (Passed=false Skipped=false)", parsed[0])
	}
	for i := 1; i < 3; i++ {
		if !parsed[i].Skipped {
			t.Errorf("result[%d].Skipped = false, want true", i)
		}
		if parsed[i].Passed {
			t.Errorf("result[%d].Passed = true, want false", i)
		}
	}
}
