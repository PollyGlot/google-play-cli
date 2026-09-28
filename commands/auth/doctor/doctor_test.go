package doctor_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRun_pureBusiness drives doctor.Run with a hand-built RunContext.
// doctor renders directly to rc.Stdout (so a failing check still prints
// the checklist), so the assertion targets the rendered bytes rather
// than the (nil) return value.
func TestRun_pureBusiness(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var stdout bytes.Buffer
	boot.Stdout = &stdout
	rc := kernel.NewForTest(ctxWithRT(successRT()), boot, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa

	r, err := doctor.Run(rc, doctor.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r != nil {
		t.Errorf("Run returned a Renderable; want nil (doctor renders itself)")
	}
	var parsed []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal: %v (raw=%q)", err, stdout.String())
	}
	if len(parsed) != 4 {
		t.Errorf("len(results) = %d, want 4", len(parsed))
	}
}

// fakeKeyring mirrors the status_test double: tracks no state by default
// but can be flipped to "unavailable" so Select() falls back to the file
// backend. The fake is what keeps the OS keystore out of unit runs.
type fakeKeyring struct {
	mu          sync.Mutex
	store       map[string]string
	unavailable bool
}

func newFakeKeyring(unavailable bool) *fakeKeyring {
	return &fakeKeyring{store: map[string]string{}, unavailable: unavailable}
}

func (f *fakeKeyring) key(service, user string) string { return service + "\x00" + user }

func (f *fakeKeyring) Set(service, user, pass string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return errors.New("keystore unavailable")
	}
	f.store[f.key(service, user)] = pass
	return nil
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return "", errors.New("keystore unavailable")
	}
	v, ok := f.store[f.key(service, user)]
	if !ok {
		return "", keystore.ErrKeyringNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return errors.New("keystore unavailable")
	}
	if _, ok := f.store[f.key(service, user)]; !ok {
		return keystore.ErrKeyringNotFound
	}
	delete(f.store, f.key(service, user))
	return nil
}

func newBoot(t *testing.T) kernel.Boot {
	t.Helper()
	// Hermetic env: doctor resolves credentials via the same resolver as
	// every other command, so a stray GPLAY_* in the developer's shell
	// must not bleed into these tests.
	t.Setenv(resolver.EnvServiceAccount, "")
	t.Setenv(resolver.EnvAccount, "")
	// Reset the package-global Select cache so each test picks the
	// backend appropriate to its fake keyring.
	root := t.TempDir()
	return kernel.Boot{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
		// Default: keyring unavailable so the file backend is used and
		// the existing setup (seeded SA on disk) works unchanged.
		Keyring: newFakeKeyring(true),
	}
}

// signedSAJSON produces a service-account JSON whose private_key is a
// real RSA key so the OAuth2 library can sign the
// exchange JWT in tests.
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

// seedActiveAccount writes a service account into whichever backend
// Select chooses for the given Boot. Using Select (vs. NewFileBackend
// directly) keeps the seed in step with what the command itself will
// read: i.e. the test exercises the same code path as production.
func seedActiveAccount(t *testing.T, boot kernel.Boot, saBytes []byte) {
	t.Helper()
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{
		Keyring:  boot.Keyring,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatalf("keystore.Select: %v", err)
	}
	if err := be.Save(context.Background(), "playci", saBytes); err != nil {
		t.Fatalf("keystore.Save: %v", err)
	}
	cfg := &config.Global{}
	cfg.AddAccount("playci")
	if err := cfg.SetActive("playci"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}
}

func runCmd(t *testing.T, boot kernel.Boot, ctx context.Context, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	boot.Stdout = stdout
	boot.Stderr = stderr
	cmd := doctor.NewCommand(boot)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	cmd.SetContext(ctx)
	return cmd.Execute()
}

// successRT answers the OAuth2 token exchange with a healthy token so
// checks 2 and 3 pass. Any other request fails its round trip: the
// package-less checks must not reach the Play API.
func successRT() http.RoundTripper {
	return testkit.NewFake()
}

// fullStackFake answers the OAuth2 token exchange plus per-package
// edits.insert and edits.delete, for command-level integration tests.
// insertByPackage sets the edits.insert status per package (200 when
// absent); every edits.delete answers 204.
func fullStackFake(insertByPackage map[string]int) *testkit.Fake {
	// /androidpublisher/v3/applications/<pkg>/edits[/<id>]
	const prefix = "/androidpublisher/v3/applications/"
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if !strings.HasPrefix(c.Path, prefix) {
			return 0, "", false
		}
		// Either "<pkg>/edits" (insert) or "<pkg>/edits/<id>" (delete).
		parts := strings.SplitN(strings.TrimPrefix(c.Path, prefix), "/edits", 2)
		if len(parts) != 2 {
			return 0, "", false
		}
		pkg := parts[0]
		switch {
		case c.Method == http.MethodPost && parts[1] == "":
			status, ok := insertByPackage[pkg]
			if !ok {
				status = http.StatusOK
			}
			if status != http.StatusOK && status != http.StatusCreated {
				return status, `{"error":{"code":` + strconv.Itoa(status) + `,"message":"upstream said no"}}`, true
			}
			return status, `{"id":"edit-for-` + pkg + `","expiryTimeSeconds":"1700000000"}`, true
		case c.Method == http.MethodDelete:
			return http.StatusNoContent, "", true
		}
		return 0, "", false
	})
}

func ctxWithRT(rt http.RoundTripper) context.Context {
	return context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
}

func TestDoctor_happyPath_prints4CheckmarksAndExits0(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))

	var stdout, stderr bytes.Buffer
	// Pin the format being asserted: this test verifies the table emoji
	// output, not the auto-default (which would be JSON in this non-TTY
	// test context).
	if err := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", "table"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	combined := stdout.String() + stderr.String()
	checks := strings.Count(combined, "✅")
	if checks != 4 {
		t.Errorf("checkmark count = %d, want 4; combined output:\n%s", checks, combined)
	}
	if strings.Contains(combined, "❌") {
		t.Errorf("combined output contains ❌ on happy path:\n%s", combined)
	}
}

func TestDoctor_defaultNonTTY_emitsJSON(t *testing.T) {
	t.Setenv("CI", "")
	outputtest.ForceTerminal(t, false)
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("non-TTY default should be JSON; got %q (err=%v)", stdout.String(), err)
	}
	if len(parsed) != 4 {
		t.Errorf("expected 4 results in auto-JSON default, got %d", len(parsed))
	}
}

func TestDoctor_defaultCIEnv_emitsJSON_evenOnTTY(t *testing.T) {
	t.Setenv("CI", "true")
	outputtest.ForceTerminal(t, true)
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &[]map[string]any{}); err != nil {
		t.Errorf("CI=true must force JSON on TTY; got %q", stdout.String())
	}
}

func TestDoctor_markdownOutput_emitsTaskList(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", "markdown"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "- [x]") {
		t.Errorf("markdown happy path missing '- [x]'; got:\n%s", out)
	}
	// Markdown checklist must not use the emoji that belongs to the table form.
	if strings.Contains(out, "✅") {
		t.Errorf("markdown output should not carry emoji ✅; got:\n%s", out)
	}
}

func TestDoctor_markdownOutput_failingCheckAndSkipped(t *testing.T) {
	boot := newBoot(t)
	bad := []byte(`{"type":"service_account","client_email":"","private_key":"","token_uri":""}`)
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{Keyring: boot.Keyring, FileRoot: boot.KeystoreRoot})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if err := be.Save(context.Background(), "playci", bad); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg := &config.Global{}
	cfg.AddAccount("playci")
	_ = cfg.SetActive("playci")
	_ = cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath)

	var stdout, stderr bytes.Buffer
	runErr := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", "markdown")
	if runErr == nil {
		t.Fatal("expected error for malformed SA")
	}
	out := stdout.String()
	if !strings.Contains(out, "- [ ]") {
		t.Errorf("markdown failure must contain '- [ ]'; got:\n%s", out)
	}
	if !strings.Contains(out, "_skipped_") {
		t.Errorf("markdown must mark skipped checks with '_skipped_'; got:\n%s", out)
	}
	if !strings.Contains(out, "(hint:") {
		t.Errorf("markdown must include hint when one is present; got:\n%s", out)
	}
}

func TestDoctor_unknownOutput_returnsErrorMentioningValidSet(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))

	var stdout, stderr bytes.Buffer
	err := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", "xml")
	if err == nil {
		t.Fatal("expected error on --output xml")
	}
	for _, want := range []string{"unsupported", "table", "json", "markdown"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestDoctor_malformedSA_failsCheck1_skipsRest(t *testing.T) {
	boot := newBoot(t)
	bad := []byte(`{"type":"service_account","client_email":"","private_key":"","token_uri":""}`)
	// Seed with bytes the keystore accepts but the doctor will detect as
	// missing required fields at resolution time.
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{
		Keyring:  boot.Keyring,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if err := be.Save(context.Background(), "playci", bad); err != nil {
		t.Fatalf("keystore.Save: %v", err)
	}
	cfg := &config.Global{}
	cfg.AddAccount("playci")
	if err := cfg.SetActive("playci"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}

	var stdout, stderr bytes.Buffer
	runErr := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr)
	if runErr == nil {
		t.Fatal("Execute: expected error on malformed SA, got nil")
	}
	if got := exit.For(runErr); got != 10 {
		t.Errorf("exit.For(err) = %d, want 10", got)
	}
}

func TestDoctor_jsonOutput_passesThroughCheckResults(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", "json"); err != nil {
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
	if len(parsed) != 4 {
		t.Fatalf("len(parsed) = %d, want 4", len(parsed))
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
	boot := newBoot(t)
	bad := []byte(`{"type":"service_account","client_email":"","private_key":"","token_uri":""}`)
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{
		Keyring:  boot.Keyring,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if err := be.Save(context.Background(), "playci", bad); err != nil {
		t.Fatalf("keystore.Save: %v", err)
	}
	cfg := &config.Global{}
	cfg.AddAccount("playci")
	if err := cfg.SetActive("playci"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}

	var stdout, stderr bytes.Buffer
	runErr := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", "json")
	if runErr == nil {
		t.Fatal("Execute: expected error on malformed SA, got nil")
	}
	if got := exit.For(runErr); got != 10 {
		t.Errorf("exit.For(err) = %d, want 10", got)
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
	if len(parsed) != 4 {
		t.Fatalf("len(parsed) = %d, want 4", len(parsed))
	}
	if parsed[0].Passed || parsed[0].Skipped {
		t.Errorf("result[0] = %+v, want first check failed (Passed=false Skipped=false)", parsed[0])
	}
	for i := 1; i < 4; i++ {
		if !parsed[i].Skipped {
			t.Errorf("result[%d].Skipped = false, want true", i)
		}
		if parsed[i].Passed {
			t.Errorf("result[%d].Passed = true, want false", i)
		}
	}
}

func TestDoctor_twoPackages_bothPassing_returns6ResultsAndExit0(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))

	ctx := ctxWithRT(fullStackFake(map[string]int{"com.example.app1": 200, "com.example.app2": 200}))

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, ctx, &stdout, &stderr,
		"--output", "json",
		"--package", "com.example.app1",
		"--package", "com.example.app2",
	); err != nil {
		t.Fatalf("Execute: %v (stderr=%s)", err, stderr.String())
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
	if len(parsed) != 6 {
		t.Fatalf("len(parsed) = %d, want 6 (4 non-API + 2 per-package)", len(parsed))
	}
	for i, r := range parsed {
		if !r.Passed {
			t.Errorf("result[%d] %s: Passed=false (%+v)", i, r.Name, r)
		}
	}
	// The per-package round-trip entries (after the 4 non-API checks) must each
	// carry the package name in their Name.
	if !strings.Contains(parsed[4].Name, "com.example.app1") {
		t.Errorf("parsed[4].Name = %q, want to contain com.example.app1", parsed[4].Name)
	}
	if !strings.Contains(parsed[5].Name, "com.example.app2") {
		t.Errorf("parsed[5].Name = %q, want to contain com.example.app2", parsed[5].Name)
	}
}

func TestDoctor_twoPackages_one403_overallExit11(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))

	ctx := ctxWithRT(fullStackFake(map[string]int{"com.example.app1": 200, "com.example.app2": 403}))

	var stdout, stderr bytes.Buffer
	runErr := runCmd(t, boot, ctx, &stdout, &stderr,
		"--output", "json",
		"--package", "com.example.app1",
		"--package", "com.example.app2",
	)
	if runErr == nil {
		t.Fatal("Execute: expected non-nil error when one package fails")
	}
	if got := exit.For(runErr); got != 11 {
		t.Errorf("exit.For(err) = %d, want 11 (worst non-zero across checks)", got)
	}
}

// TestDoctor_corruptActiveCred_missingField_check1ShowsRealCause asserts
// the ADR-0020 behavioral delta: a corrupt active credential (missing a
// required field) makes check 1 surface the REAL resolution cause:
// the field-named hint, not the synthetic "no Account resolved" message.
func TestDoctor_corruptActiveCred_missingField_check1ShowsRealCause(t *testing.T) {
	boot := newBoot(t)
	// Valid JSON, accepted by the keystore, but missing client_email so
	// resolution fails with a MissingFieldError on the production path.
	seedActiveAccount(t, boot, []byte(`{"type":"service_account"}`))

	var stdout, stderr bytes.Buffer
	runErr := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", "table")
	if runErr == nil {
		t.Fatal("Execute: expected error on corrupt active credential, got nil")
	}
	if got := exit.For(runErr); got != 10 {
		t.Errorf("exit.For(err) = %d, want 10", got)
	}
	out := stdout.String()
	if !strings.Contains(out, `missing required field "client_email"`) {
		t.Errorf("check 1 must surface the real cause (field-named hint); got:\n%s", out)
	}
	if strings.Contains(out, "no Account resolved") {
		t.Errorf("check 1 must not fall back to the synthetic absent message; got:\n%s", out)
	}
	// The checklist is rendered exactly once: no double-report of check 1.
	if n := strings.Count(out, "Service account JSON is valid"); n != 1 {
		t.Errorf("check-1 name appears %d times, want exactly 1; got:\n%s", n, out)
	}
}

// TestDoctor_corruptActiveCred_malformedJSON_hintCarriesCause asserts that
// when the active credential is malformed JSON, check 1's hint carries the
// underlying resolution cause ("could not read credential: ...").
func TestDoctor_corruptActiveCred_malformedJSON_hintCarriesCause(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot, []byte("{ not json"))

	var stdout, stderr bytes.Buffer
	runErr := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", "table")
	if runErr == nil {
		t.Fatal("Execute: expected error on malformed active credential, got nil")
	}
	if got := exit.For(runErr); got != 10 {
		t.Errorf("exit.For(err) = %d, want 10", got)
	}
	if out := stdout.String(); !strings.Contains(out, "could not read credential") {
		t.Errorf("check 1 hint must carry the resolution cause; got:\n%s", out)
	}
}

// TestDoctor_absentCred_check1IsGenericSynthetic is a regression guard: with
// no credential at all (absent), check 1 carries the one no-Account wording
// (kernel.NoAccountError) rather than a resolution-error cause.
func TestDoctor_absentCred_check1IsGenericSynthetic(t *testing.T) {
	boot := newBoot(t) // no seeding → genuinely absent

	var stdout, stderr bytes.Buffer
	runErr := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", "table")
	if runErr == nil {
		t.Fatal("Execute: expected error when no credential is present, got nil")
	}
	if got := exit.For(runErr); got != 10 {
		t.Errorf("exit.For(err) = %d, want 10", got)
	}
	if out := stdout.String(); !strings.Contains(out, "no Account resolved: run `gplay auth login`") {
		t.Errorf("absent case must keep the generic synthetic message; got:\n%s", out)
	}
}

func TestDoctor_withoutPackage_runsOnlyFourChecks(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var parsed []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(parsed) != 4 {
		t.Errorf("len(parsed) = %d, want 4 (no --package → only non-API checks)", len(parsed))
	}
}
