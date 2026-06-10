package status_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/auth/status"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRun_pureBusiness drives status.Run with a hand-built RunContext.
// Run pulls Account/Resolved/KeystoreLabel from rc and shapes Payload.
func TestRun_pureBusiness(t *testing.T) {
	rc := kernel.NewForTest(context.Background(), kernel.Boot{KeystoreRoot: "/keys"}, kernel.Inputs{})
	rc.Account = &serviceaccount.ServiceAccount{ClientEmail: "playci@x"}
	rc.AccountName = "playci" // the credential in use — status keys the name off this
	rc.Resolved = &config.Resolved{ConfigAccount: "playci"}
	rc.KeystoreLabel = keystore.BackendFile

	r, err := status.Run(rc, status.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p, ok := r.(status.Payload)
	if !ok {
		t.Fatalf("Run returned %T, want status.Payload", r)
	}
	if !p.Active || p.Name != "playci" || p.ClientEmail != "playci@x" {
		t.Errorf("payload = %+v, want active=true name=playci email=playci@x", p)
	}
}

// fakeKeyring is a minimal in-process double for keystore.KeyringAPI used by
// the status tests. Each test constructs a fresh one — never touches the
// real OS keystore.
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

const validSAJSON = `{
  "type": "service_account",
  "project_id": "test-proj",
  "private_key": "-----BEGIN PRIVATE KEY-----\nX\n-----END PRIVATE KEY-----\n",
  "client_email": "playci@test-proj.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

func newBoot(t *testing.T, kr keystore.KeyringAPI) kernel.Boot {
	t.Helper()
	// Hermetic env: the status command resolves credentials via the same
	// resolver as everywhere else, so a stray GPLAY_* in the developer's
	// shell must not bleed into these tests.
	t.Setenv(resolver.EnvServiceAccount, "")
	t.Setenv(resolver.EnvAccount, "")
	// Reset the package-global Select cache so each test picks the
	// backend appropriate to its fake keyring.
	root := t.TempDir()
	return kernel.Boot{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
		Keyring:      kr,
	}
}

// seedActiveAccount writes a service account into whichever backend Select
// chooses for the given Boot. Using Select (vs. NewFileBackend directly)
// keeps the seed in step with what the command itself will read.
func seedActiveAccount(t *testing.T, boot kernel.Boot) {
	t.Helper()
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{
		Keyring:  boot.Keyring,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := be.Save(context.Background(), "playci", []byte(validSAJSON)); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Global{}
	cfg.AddAccount("playci")
	if err := cfg.SetActive("playci"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
		t.Fatal(err)
	}
}

// seedCorruptAccount mirrors seedActiveAccount but Saves badBytes (instead of
// validSAJSON) under "playci" into the Select-chosen backend, then writes a
// global config with "playci" active. It models an active Account whose stored
// credential is *invalid* (ADR-0020): present but unusable.
func seedCorruptAccount(t *testing.T, boot kernel.Boot, badBytes []byte) {
	t.Helper()
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{
		Keyring:  boot.Keyring,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := be.Save(context.Background(), "playci", badBytes); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Global{}
	cfg.AddAccount("playci")
	if err := cfg.SetActive("playci"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
		t.Fatal(err)
	}
}

func runCmd(t *testing.T, boot kernel.Boot, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	boot.Stdout = stdout
	boot.Stderr = stderr
	// Wrap the subcommand in a minimal root so the persistent --verbose
	// flag (declared at the binary level) is in scope, matching the
	// production wiring in cmd/gplay/main.go.
	sub := status.NewCommand(boot)
	root := &cobra.Command{Use: "gplay"}
	root.PersistentFlags().BoolP("verbose", "v", false, "")
	root.AddCommand(sub)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"status"}, args...))
	return root.Execute()
}

func TestStatus_fileBackend_tableShowsNameEmailBackendAndPath(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	// Tests target the table rendering. The auto-default resolves to JSON
	// here (test buffer is not a TTY), so we pass --output table to pin
	// the format being asserted.
	if err := runCmd(t, boot, &stdout, &stderr, "--output", "table"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "playci") {
		t.Errorf("output missing account name; got %q", out)
	}
	if !strings.Contains(out, "playci@test-proj.iam.gserviceaccount.com") {
		t.Errorf("output missing client_email; got %q", out)
	}
	wantPath := filepath.Join(boot.KeystoreRoot, "playci.json")
	if !strings.Contains(out, wantPath) {
		t.Errorf("output missing credential path; got %q", out)
	}
	// Backend label: per the issue spec, "file: <path>" when file-backed.
	if !strings.Contains(out, "file") {
		t.Errorf("output missing 'file' backend label; got %q", out)
	}
}

func TestStatus_fileBackend_jsonOutputIncludesBackendAndPath(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Name        string `json:"name"`
		ClientEmail string `json:"client_email"`
		Backend     string `json:"backend"`
		Path        string `json:"path"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, stdout.String())
	}
	if payload.Name != "playci" {
		t.Errorf("json.name = %q, want playci", payload.Name)
	}
	if payload.ClientEmail != "playci@test-proj.iam.gserviceaccount.com" {
		t.Errorf("json.client_email = %q", payload.ClientEmail)
	}
	if payload.Backend != "file" {
		t.Errorf("json.backend = %q, want file", payload.Backend)
	}
	if payload.Path != filepath.Join(boot.KeystoreRoot, "playci.json") {
		t.Errorf("json.path = %q", payload.Path)
	}
}

func TestStatus_keyringBackend_displaysKeyringLabelAndOmitsPath(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(false))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "table"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, keystore.BackendKeyring) {
		t.Errorf("output missing %q backend label; got %q", keystore.BackendKeyring, out)
	}
	// File path is meaningless when the keyring is active and must not
	// appear — otherwise users go hunting for a file that doesn't exist.
	if strings.Contains(out, filepath.Join(boot.KeystoreRoot, "playci.json")) {
		t.Errorf("output should omit file path when keyring is active; got %q", out)
	}
}

func TestStatus_keyringBackend_jsonOmitsPath(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(false))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Name    string `json:"name"`
		Backend string `json:"backend"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if payload.Backend != "keyring" {
		t.Errorf("json.backend = %q, want keyring", payload.Backend)
	}
	if payload.Path != "" {
		t.Errorf("json.path = %q, want empty for keyring backend", payload.Path)
	}
}

func TestStatus_verboseFlag_emitsBackendSelectionLog(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "-v"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	want := "keystore: using file backend"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr missing backend selection log %q; got %q", want, stderr.String())
	}
}

func TestStatus_withoutVerbose_isSilentOnBackendSelection(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.Contains(stderr.String(), "keystore: using") {
		t.Errorf("non-verbose status should not emit backend log; got stderr %q", stderr.String())
	}
}

func TestStatus_noActiveAccount_printsInformationalAndExitsZero(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "table"); err != nil {
		t.Fatalf("Execute: expected nil error (informational state), got %v", err)
	}
	if !strings.Contains(stdout.String(), "No active account") {
		t.Errorf("stdout missing 'No active account' line; got %q", stdout.String())
	}
}

func TestStatus_defaultNonTTY_emitsJSON(t *testing.T) {
	t.Setenv("CI", "")
	outputtest.ForceTerminal(t, false)
	boot := newBoot(t, newFakeKeyring(true))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("non-TTY default should be JSON; got %q (err=%v)", stdout.String(), err)
	}
	if payload.Name != "playci" {
		t.Errorf("json.name = %q, want playci", payload.Name)
	}
}

func TestStatus_defaultCIEnv_emitsJSON_evenOnTTY(t *testing.T) {
	t.Setenv("CI", "true")
	outputtest.ForceTerminal(t, true)
	boot := newBoot(t, newFakeKeyring(true))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &struct{}{}); err != nil {
		t.Errorf("CI=true should force JSON even on TTY; got %q", stdout.String())
	}
}

func TestStatus_defaultTTY_emitsTable(t *testing.T) {
	t.Setenv("CI", "")
	outputtest.ForceTerminal(t, true)
	boot := newBoot(t, newFakeKeyring(true))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "Active account:") {
		t.Errorf("TTY default should be table; got %q", stdout.String())
	}
}

func TestStatus_markdownOutput_emitsDefinitionList(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "markdown"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"- **Active account**: playci",
		"- **Client email**: playci@test-proj.iam.gserviceaccount.com",
		"- **Backend**: file",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q; got %q", want, out)
		}
	}
}

func TestStatus_markdownOutput_emptyShape(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "markdown"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "**No active account.**") {
		t.Errorf("empty markdown missing 'No active account' header; got %q", out)
	}
	if !strings.Contains(out, "gplay auth login") {
		t.Errorf("empty markdown missing login hint; got %q", out)
	}
}

func TestStatus_explicitTableInPipe_overridesAutoJSON(t *testing.T) {
	t.Setenv("CI", "")
	outputtest.ForceTerminal(t, false)
	boot := newBoot(t, newFakeKeyring(true))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "table"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "Active account:") {
		t.Errorf("explicit --output table must win even in pipe; got %q", stdout.String())
	}
}

func TestStatus_unknownOutput_returnsErrorMentioningValidSet(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seedActiveAccount(t, boot)
	var stdout, stderr bytes.Buffer

	err := runCmd(t, boot, &stdout, &stderr, "--output", "xml")
	if err == nil {
		t.Fatal("expected error on --output xml, got nil")
	}
	for _, want := range []string{"unsupported", "table", "json", "markdown"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestStatus_inlineCredential_reportsEnvOverrideWithoutProbing locks in the
// fix for the reintroduced keychain probe: an inline GPLAY_SERVICE_ACCOUNT
// credential resolves without the keystore, so status must report it as an
// env override and leave the backend label empty rather than probing (which,
// with this unavailable keyring, would have surfaced "file").
func TestStatus_inlineCredential_reportsEnvOverrideWithoutProbing(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true)) // a probe here would yield "file"
	t.Setenv(resolver.EnvServiceAccount, validSAJSON)
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload struct {
		Active  bool   `json:"active"`
		Name    string `json:"name"`
		Backend string `json:"backend"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, stdout.String())
	}
	if !payload.Active {
		t.Errorf("json.active = false, want true for an inline credential")
	}
	if payload.Name != "(env override)" {
		t.Errorf("json.name = %q, want \"(env override)\"", payload.Name)
	}
	if payload.Backend != "" {
		t.Errorf("json.backend = %q, want empty — an inline credential must not probe the keystore", payload.Backend)
	}
}

// TestStatus_corruptActiveCredential_malformedJSON_isExit10 locks in the
// ADR-0020 invalid path: an active Account whose stored bytes are not valid
// JSON must hard-error (exit 10) with the "could not read credential" message
// and render no payload — status must not paper over a corrupt credential.
func TestStatus_corruptActiveCredential_malformedJSON_isExit10(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seedCorruptAccount(t, boot, []byte("{ not valid json"))
	var stdout, stderr bytes.Buffer

	err := runCmd(t, boot, &stdout, &stderr, "--output", "json")
	if err == nil {
		t.Fatal("expected an error for a corrupt active credential, got nil")
	}
	if code := exit.For(err); code != 10 {
		t.Errorf("exit.For(err) = %d, want 10", code)
	}
	if !strings.Contains(err.Error(), "could not read credential") {
		t.Errorf("error %q missing 'could not read credential'", err.Error())
	}
	// Lock in the cause-preservation contract (%w): the wrapped JSON parse
	// error survives, not just the wrapper prefix.
	if !strings.Contains(err.Error(), "invalid character") {
		t.Errorf("error %q should preserve the underlying JSON parse cause", err.Error())
	}
	// Under --output json a failure emits the structured error envelope on
	// stdout (ADR-0023): it must be exactly that (exit 10 + the real cause) and
	// must NOT paper over the corrupt credential with a status payload. The
	// exit-10 envelope assertion below would not hold if status had instead
	// rendered a fabricated `{}`/account payload (that path returns exit 0).
	if out := stdout.String(); !strings.Contains(out, `"exitCode": 10`) {
		t.Fatalf("json hard-error path should emit the exit-10 error envelope; got %q", out)
	}
}

// TestStatus_corruptActiveCredential_missingField_namesTheCause checks that the
// typed cause survives the credentialError wrapping (Unwrap): a service account
// missing client_email surfaces exit 10 with the field name in the message.
func TestStatus_corruptActiveCredential_missingField_namesTheCause(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seedCorruptAccount(t, boot, []byte(`{"type":"service_account"}`))
	var stdout, stderr bytes.Buffer

	err := runCmd(t, boot, &stdout, &stderr, "--output", "json")
	if err == nil {
		t.Fatal("expected an error for a credential missing client_email, got nil")
	}
	if code := exit.For(err); code != 10 {
		t.Errorf("exit.For(err) = %d, want 10", code)
	}
	if !strings.Contains(err.Error(), "client_email") {
		t.Errorf("error %q should name the missing field client_email", err.Error())
	}
}

// TestStatus_loggedOutActiveAccount_staysExitZero covers the ADR-0020 absent
// path: a config naming "playci" active but with no key in the store resolves
// to ErrNotFound (benign), so status exits 0 with "No active account" rather
// than erroring. The unavailable keyring forces the file backend; no key file
// exists, so Load returns ErrNotFound.
func TestStatus_loggedOutActiveAccount_staysExitZero(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	cfg := &config.Global{}
	cfg.AddAccount("playci")
	if err := cfg.SetActive("playci"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "table"); err != nil {
		t.Fatalf("Execute: expected nil error (absent credential), got %v", err)
	}
	if !strings.Contains(stdout.String(), "No active account") {
		t.Errorf("stdout missing 'No active account' line; got %q", stdout.String())
	}
}

func TestStatus_noActiveAccount_jsonShowsActiveFalse(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	var stdout, stderr bytes.Buffer

	if err := runCmd(t, boot, &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload struct {
		Active bool   `json:"active"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, stdout.String())
	}
	if payload.Active {
		t.Errorf("json.active = true, want false")
	}
	if payload.Name != "" {
		t.Errorf("json.name = %q, want empty", payload.Name)
	}
}
