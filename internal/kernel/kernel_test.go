package kernel_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// TestGroupRunE_bareInvocationPrintsHelp asserts the bare-group contract: with
// no leftover args, GroupRunE prints the command's help and returns nil (the
// command exits 0). cobra routes Help() to the command's configured output.
func TestGroupRunE_bareInvocationPrintsHelp(t *testing.T) {
	var out bytes.Buffer
	cmd := &cobra.Command{Use: "grp", Short: "a grouping noun"}
	cmd.SetOut(&out)
	if err := kernel.GroupRunE(cmd, nil); err != nil {
		t.Fatalf("GroupRunE(bare) = %v, want nil (help + exit 0)", err)
	}
	if out.Len() == 0 {
		t.Errorf("GroupRunE(bare) printed no help")
	}
}

// TestGroupRunE_unknownSubcommandIsMisuse asserts the unknown-subcommand
// contract: a leftover token is surfaced as a *exit.UsageError (exit 2 per
// docs/DESIGN.md §9) whose message names both the unknown token and the group
// path — so a typo or a removed verb fails loudly rather than silently helping.
func TestGroupRunE_unknownSubcommandIsMisuse(t *testing.T) {
	cmd := &cobra.Command{Use: "grp"}
	err := kernel.GroupRunE(cmd, []string{"nonesuch", "extra"})
	if err == nil {
		t.Fatal("GroupRunE(unknown) = nil, want a misuse error")
	}
	if code := exit.For(err); code != 2 {
		t.Errorf("exit.For = %d, want 2 (CLI misuse); err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "unknown command") || !strings.Contains(err.Error(), "nonesuch") {
		t.Errorf("error = %q, want it to name the unknown command %q", err, "nonesuch")
	}
}

const fakeSAJSON = `{
  "type": "service_account",
  "project_id": "p",
  "private_key": "-----BEGIN PRIVATE KEY-----\nXX\n-----END PRIVATE KEY-----\n",
  "client_email": "ci@p.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

// unavailableKeyring is a KeyringAPI whose probe always fails — Select
// then falls back to the file backend.
type unavailableKeyring struct{}

func (unavailableKeyring) Set(string, string, string) error { return errBoom }
func (unavailableKeyring) Get(string, string) (string, error) {
	return "", errBoom
}
func (unavailableKeyring) Delete(string, string) error { return errBoom }

// countingKeyring is an in-memory KeyringAPI that tallies every call so a
// test can assert exactly when (and whether) a run reaches the OS keyring.
// Its probe succeeds (Set/Delete return nil), so Select picks the keyring
// backend — the credential round-trips through `store`.
type countingKeyring struct {
	store            map[string]string
	sets, gets, dels int
}

func newAvailableKeyring() *countingKeyring { return &countingKeyring{store: map[string]string{}} }

func (k *countingKeyring) mapKey(service, user string) string { return service + "\x00" + user }

func (k *countingKeyring) Set(service, user, password string) error {
	k.sets++
	if k.store == nil {
		k.store = map[string]string{}
	}
	k.store[k.mapKey(service, user)] = password
	return nil
}

func (k *countingKeyring) Get(service, user string) (string, error) {
	k.gets++
	v, ok := k.store[k.mapKey(service, user)]
	if !ok {
		return "", keystore.ErrKeyringNotFound
	}
	return v, nil
}

func (k *countingKeyring) Delete(service, user string) error {
	k.dels++
	delete(k.store, k.mapKey(service, user))
	return nil
}

func (k *countingKeyring) calls() int { return k.sets + k.gets + k.dels }
func (k *countingKeyring) reset()     { k.sets, k.gets, k.dels = 0, 0, 0 }

var errBoom = stringError("keyring unavailable")

type stringError string

func (s stringError) Error() string { return string(s) }

func newBoot(t *testing.T) kernel.Boot {
	t.Helper()
	t.Setenv(resolver.EnvServiceAccount, "")
	t.Setenv(resolver.EnvAccount, "")
	root := t.TempDir()
	return kernel.Boot{
		Stdout:       &bytes.Buffer{},
		Stderr:       &bytes.Buffer{},
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
		Keyring:      unavailableKeyring{},
	}
}

func seedActiveAccount(t *testing.T, boot kernel.Boot) {
	t.Helper()
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{
		Keyring: boot.Keyring, FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if err := be.Save(context.Background(), "ci", []byte(fakeSAJSON)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg := &config.Global{}
	cfg.AddAccount("ci")
	if err := cfg.SetActive("ci"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}
}

// fakePayload is a minimal Renderable used to assert Run wires the
// rendering dispatch correctly.
type fakePayload struct{ msg string }

func (p fakePayload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { _, err := io.WriteString(w, "table:"+p.msg); return err },
		JSON:     func(w io.Writer) error { _, err := io.WriteString(w, `{"msg":"`+p.msg+`"}`); return err },
		Markdown: func(w io.Writer) error { _, err := io.WriteString(w, "- "+p.msg); return err },
	}
}

func TestNew_wiresIOFromBoot(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{})
	if rc.Stdout != boot.Stdout {
		t.Errorf("Stdout not wired from Boot")
	}
	if rc.Stderr != boot.Stderr {
		t.Errorf("Stderr not wired from Boot")
	}
	if rc.ConfigPath != boot.ConfigPath {
		t.Errorf("ConfigPath mismatch")
	}
}

func TestRun_resolvesActiveAccountAndRenders(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot)
	var stdout bytes.Buffer
	boot.Stdout = &stdout

	err := kernel.Run(boot, kernel.Inputs{Format: output.FormatJSON}, func(rc *kernel.RunContext) (output.Renderable, error) {
		// Resolution is lazy now: the credential lands only once a command
		// asks for it (here, explicitly; in production via AuthedClient).
		rc.EnsureAccount()
		if rc.Account == nil {
			t.Fatal("rc.Account is nil after EnsureAccount on a seeded active account")
		}
		if rc.Account.ClientEmail != "ci@p.iam.gserviceaccount.com" {
			t.Errorf("ClientEmail = %q", rc.Account.ClientEmail)
		}
		return fakePayload{msg: "ok"}, nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), `"msg":"ok"`) {
		t.Errorf("stdout did not get the JSON rendering; got %q", stdout.String())
	}
}

func TestRun_noAccount_callsFnWithNilAccount(t *testing.T) {
	boot := newBoot(t)
	called := false
	err := kernel.Run(boot, kernel.Inputs{}, func(rc *kernel.RunContext) (output.Renderable, error) {
		called = true
		rc.EnsureAccount()
		if rc.Account != nil {
			t.Errorf("rc.Account = %+v, want nil when nothing resolves", rc.Account)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Error("fn was not invoked")
	}
}

func TestRun_fnError_propagates(t *testing.T) {
	boot := newBoot(t)
	want := errors.New("boom")
	err := kernel.Run(boot, kernel.Inputs{}, func(rc *kernel.RunContext) (output.Renderable, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestRun_nilRenderable_noRender(t *testing.T) {
	boot := newBoot(t)
	var stdout bytes.Buffer
	boot.Stdout = &stdout
	err := kernel.Run(boot, kernel.Inputs{Format: output.FormatJSON}, func(rc *kernel.RunContext) (output.Renderable, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty when fn returns nil Renderable", stdout.String())
	}
}

func TestRun_verbose_logsBackendOnce(t *testing.T) {
	boot := newBoot(t)
	var stderr bytes.Buffer
	boot.Stderr = &stderr
	if err := kernel.Run(boot, kernel.Inputs{Verbose: true}, func(rc *kernel.RunContext) (output.Renderable, error) {
		// The backend label is logged when the keystore is first used, not
		// at boot — force selection twice to prove the log is memoised to
		// exactly one line.
		if _, err := rc.Backend(); err != nil {
			return nil, err
		}
		if _, err := rc.Backend(); err != nil {
			return nil, err
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c := strings.Count(stderr.String(), "keystore: using"); c != 1 {
		t.Errorf("verbose: backend label logged %d times, want 1; stderr=%q", c, stderr.String())
	}
}

// TestRun_verbose_preAuthIsProbeFree is the regression guard for the macOS
// "keychain locked" dialog: a -v command that returns before needing a
// credential must neither log a backend nor touch the keyring.
func TestRun_verbose_preAuthIsProbeFree(t *testing.T) {
	boot := newBoot(t)
	spy := &countingKeyring{}
	boot.Keyring = spy
	var stderr bytes.Buffer
	boot.Stderr = &stderr
	if err := kernel.Run(boot, kernel.Inputs{Verbose: true}, func(rc *kernel.RunContext) (output.Renderable, error) {
		// Stand in for a validation failure / --help / --dry-run: never
		// asks for a credential.
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := spy.calls(); n != 0 {
		t.Errorf("pre-auth command touched the keyring %d times, want 0", n)
	}
	if strings.Contains(stderr.String(), "keystore: using") {
		t.Errorf("pre-auth -v should not log a backend; got stderr %q", stderr.String())
	}
}

// TestRun_authNeedingCommand_probesKeyring is the positive counterpart:
// once a command actually resolves a stored credential, the keyring IS
// reached (probe + Load).
func TestRun_authNeedingCommand_probesKeyring(t *testing.T) {
	boot := newBoot(t)
	spy := newAvailableKeyring()
	boot.Keyring = spy
	seedActiveAccount(t, boot) // routes the credential into the spy keyring
	spy.reset()

	if err := kernel.Run(boot, kernel.Inputs{}, func(rc *kernel.RunContext) (output.Renderable, error) {
		rc.EnsureAccount()
		if rc.Account == nil {
			t.Fatal("rc.Account is nil after EnsureAccount on a seeded keyring Account")
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if spy.gets == 0 {
		t.Errorf("auth-needing command never read the keyring; calls=%d", spy.calls())
	}
}
