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
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
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

// faultyGetKeyring passes Select's Set+Delete probe (so the keyring backend
// is chosen) but fails every Get with a generic, non-NotFound error. It
// stands in for a keyring that is reachable yet returns an IO/permission
// fault when a stored credential is actually read — the "invalid" bucket's
// non-NotFound keystore-error case (ADR-0020).
type faultyGetKeyring struct{}

func (faultyGetKeyring) Set(string, string, string) error { return nil }
func (faultyGetKeyring) Get(string, string) (string, error) {
	return "", errBoom
}
func (faultyGetKeyring) Delete(string, string) error { return nil }

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

// seedActiveConfigOnly writes a global config whose active Account is "ci"
// but stores NO key for it — so resolution reaches the keystore and gets
// back keystore.ErrNotFound (the logged-out / never-stored case). Used to
// distinguish the benign-absent path from a present-but-invalid credential.
func seedActiveConfigOnly(t *testing.T, boot kernel.Boot) {
	t.Helper()
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

// ---- EnsureAccount: absent / invalid / valid trichotomy (ADR-0020) ----
//
// These drive the production lazy path (kernel.Run → buildRunContext,
// lazy=true) and assert only external behavior: the error EnsureAccount
// returns, its exit.For code, and whether rc.Account stayed nil — never the
// private accountErr/accountDone fields. The three states are injected
// through the real resolution seams (resolver.Inputs, the keyring fakes, an
// OSFS config), exactly as a command would hit them.

// ensureViaRun runs EnsureAccount on a production (lazy) RunContext and
// reports what it returned plus whether rc.Account stayed nil. The fn never
// returns an error, so a non-nil kernel.Run result is a test-rig failure,
// not the behavior under test.
func ensureViaRun(t *testing.T, boot kernel.Boot, in kernel.Inputs) (ensureErr error, accountNil bool) {
	t.Helper()
	if runErr := kernel.Run(boot, in, func(rc *kernel.RunContext) (output.Renderable, error) {
		ensureErr = rc.EnsureAccount()
		accountNil = rc.Account == nil
		return nil, nil
	}); runErr != nil {
		t.Fatalf("kernel.Run rig error: %v", runErr)
	}
	return ensureErr, accountNil
}

func TestEnsureAccount_absent_noSource_returnsNilErrorAndNilAccount(t *testing.T) {
	boot := newBoot(t) // hermetic: no env, no config, unavailable keyring
	err, accountNil := ensureViaRun(t, boot, kernel.Inputs{})
	if err != nil {
		t.Errorf("EnsureAccount with no source = %v, want nil (benign absent)", err)
	}
	if !accountNil {
		t.Error("rc.Account should stay nil when nothing resolves")
	}
}

func TestEnsureAccount_absent_loggedOutActiveAccount_returnsNilError(t *testing.T) {
	boot := newBoot(t)
	seedActiveConfigOnly(t, boot) // active "ci", but no key in the store
	err, accountNil := ensureViaRun(t, boot, kernel.Inputs{})
	if err != nil {
		t.Errorf("EnsureAccount on a logged-out active Account = %v, want nil (ErrNotFound is absent)", err)
	}
	if !accountNil {
		t.Error("rc.Account should stay nil for a logged-out active Account")
	}
}

func TestEnsureAccount_invalid_malformedInlineJSON_isExit10(t *testing.T) {
	boot := newBoot(t)
	err, accountNil := ensureViaRun(t, boot, kernel.Inputs{
		Resolver: resolver.Inputs{EnvServiceAccount: "{ this is not valid json"},
	})
	if err == nil {
		t.Fatal("EnsureAccount with malformed inline JSON = nil, want an invalid-credential error")
	}
	if got := exit.For(err); got != 10 {
		t.Errorf("exit.For = %d, want 10; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "could not read credential") {
		t.Errorf("error %q should carry the 'could not read credential' prefix", err.Error())
	}
	if !accountNil {
		t.Error("rc.Account must stay nil on an invalid credential")
	}
}

func TestEnsureAccount_invalid_missingField_preservesTypedCause(t *testing.T) {
	boot := newBoot(t)
	// Valid JSON, but client_email (the first required field) is absent.
	err, _ := ensureViaRun(t, boot, kernel.Inputs{
		Resolver: resolver.Inputs{EnvServiceAccount: `{"type":"service_account"}`},
	})
	if err == nil {
		t.Fatal("EnsureAccount with a missing required field = nil, want an error")
	}
	if got := exit.For(err); got != 10 {
		t.Errorf("exit.For = %d, want 10; err=%v", got, err)
	}
	var mfe *serviceaccount.MissingFieldError
	if !errors.As(err, &mfe) {
		t.Fatalf("error %v should wrap a *serviceaccount.MissingFieldError (errors.As)", err)
	}
	if mfe.Field != "client_email" {
		t.Errorf("MissingFieldError.Field = %q, want client_email", mfe.Field)
	}
}

func TestEnsureAccount_invalid_unreadableFile_isExit10(t *testing.T) {
	boot := newBoot(t)
	missing := filepath.Join(t.TempDir(), "nope.json")
	err, accountNil := ensureViaRun(t, boot, kernel.Inputs{
		Resolver: resolver.Inputs{ServiceAccountFlag: missing},
	})
	if err == nil {
		t.Fatal("EnsureAccount with an unreadable --service-account path = nil, want an error")
	}
	if got := exit.For(err); got != 10 {
		t.Errorf("exit.For = %d, want 10; err=%v", got, err)
	}
	if !accountNil {
		t.Error("rc.Account must stay nil on an unreadable credential file")
	}
}

func TestEnsureAccount_invalid_nonNotFoundKeystoreError_isExit10(t *testing.T) {
	boot := newBoot(t)
	boot.Keyring = faultyGetKeyring{} // probe passes, the credential Get faults
	seedActiveConfigOnly(t, boot)     // active "ci" → resolution reaches the keyring Load
	err, accountNil := ensureViaRun(t, boot, kernel.Inputs{})
	if err == nil {
		t.Fatal("EnsureAccount with a faulting keystore read = nil, want an error")
	}
	if got := exit.For(err); got != 10 {
		t.Errorf("exit.For = %d, want 10; err=%v", got, err)
	}
	if !accountNil {
		t.Error("rc.Account must stay nil on a keystore read fault")
	}
}

func TestEnsureAccount_valid_returnsNilErrorAndAccount(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot)
	var got *serviceaccount.ServiceAccount
	var ensureErr error
	if err := kernel.Run(boot, kernel.Inputs{}, func(rc *kernel.RunContext) (output.Renderable, error) {
		ensureErr = rc.EnsureAccount()
		got = rc.Account
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ensureErr != nil {
		t.Errorf("EnsureAccount on a valid credential = %v, want nil", ensureErr)
	}
	if got == nil {
		t.Fatal("rc.Account is nil after EnsureAccount on a valid seeded Account")
	}
	if got.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", got.ClientEmail)
	}
}

func TestEnsureAccount_idempotent_memoisesWithoutExtraProbe(t *testing.T) {
	boot := newBoot(t)
	spy := newAvailableKeyring()
	boot.Keyring = spy
	seedActiveAccount(t, boot) // routes the credential into the spy keyring
	spy.reset()

	if err := kernel.Run(boot, kernel.Inputs{}, func(rc *kernel.RunContext) (output.Renderable, error) {
		if err := rc.EnsureAccount(); err != nil {
			t.Fatalf("first EnsureAccount = %v, want nil", err)
		}
		first := spy.calls()
		if first == 0 {
			t.Fatal("first EnsureAccount never read the keyring")
		}
		if err := rc.EnsureAccount(); err != nil {
			t.Fatalf("second EnsureAccount = %v, want nil (memoised)", err)
		}
		if second := spy.calls(); second != first {
			t.Errorf("second EnsureAccount re-probed the keyring: calls %d -> %d, want stable", first, second)
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestEnsureAccount_idempotent_memoisesInvalidError(t *testing.T) {
	boot := newBoot(t)
	in := kernel.Inputs{Resolver: resolver.Inputs{EnvServiceAccount: "{ not json"}}
	if err := kernel.Run(boot, in, func(rc *kernel.RunContext) (output.Renderable, error) {
		first := rc.EnsureAccount()
		second := rc.EnsureAccount()
		if first == nil || second == nil {
			t.Fatalf("EnsureAccount returned nil for an invalid credential: first=%v second=%v", first, second)
		}
		// Same pointer ⟹ the second call returned the memoised result rather
		// than re-resolving.
		if first != second {
			t.Errorf("EnsureAccount did not memoise the invalid error: first=%p second=%p", first, second)
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestAuthedClient_invalidCredential_propagatesCauseAndExit10 locks in the
// AuthedClient half of #177: a present-but-invalid credential surfaces its
// real cause (exit 10), not the generic "no Account resolved" login hint.
func TestAuthedClient_invalidCredential_propagatesCauseAndExit10(t *testing.T) {
	boot := newBoot(t)
	in := kernel.Inputs{Resolver: resolver.Inputs{EnvServiceAccount: "{ not json"}}
	if err := kernel.Run(boot, in, func(rc *kernel.RunContext) (output.Renderable, error) {
		_, err := rc.AuthedClient()
		if err == nil {
			t.Fatal("AuthedClient with an invalid credential = nil error, want exit 10")
		}
		if got := exit.For(err); got != 10 {
			t.Errorf("exit.For = %d, want 10; err=%v", got, err)
		}
		if !strings.Contains(err.Error(), "could not read credential") {
			t.Errorf("AuthedClient should surface the real cause; got %q", err.Error())
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestAuthedClient_absent_keepsLoginHint is the absent-bucket counterpart:
// with no credential at all, AuthedClient still returns its existing
// authError pointing at `gplay auth login` (exit 10).
func TestAuthedClient_absent_keepsLoginHint(t *testing.T) {
	boot := newBoot(t)
	if err := kernel.Run(boot, kernel.Inputs{}, func(rc *kernel.RunContext) (output.Renderable, error) {
		_, err := rc.AuthedClient()
		if err == nil {
			t.Fatal("AuthedClient with no credential = nil error, want exit 10")
		}
		if got := exit.For(err); got != 10 {
			t.Errorf("exit.For = %d, want 10; err=%v", got, err)
		}
		if !strings.Contains(err.Error(), "auth login") {
			t.Errorf("absent AuthedClient should keep the login hint; got %q", err.Error())
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
