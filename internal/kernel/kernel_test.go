package kernel_test

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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
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
		if err := rc.EnsureAccount(); err != nil {
			t.Fatalf("EnsureAccount on a seeded active account = %v, want nil", err)
		}
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
		if err := rc.EnsureAccount(); err != nil {
			t.Errorf("EnsureAccount with nothing configured = %v, want nil (benign absent)", err)
		}
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
		if err := rc.EnsureAccount(); err != nil {
			t.Fatalf("EnsureAccount on a seeded keyring Account = %v, want nil", err)
		}
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

// ---- GPLAY_READONLY policy + exit 4 (#211 / ADR-0024) ----
//
// These assert the policy contract: under GPLAY_READONLY a mutating, non-dry-run
// command is refused with exit 4 BEFORE its business function runs (so before
// credential resolution and any network I/O), while read commands and --dry-run
// of mutating commands still run. Refusal is driven by Inputs.{Readonly,
// Mutating,DryRun}; the RunCobra test exercises the annotation + env wiring.

// TestRun_readonly_refusesMutatingCommand_exit4 proves the refusal fires before
// fn (the fn would make a network call; it must never be reached), with the
// dedicated exit code 4.
func TestRun_readonly_refusesMutatingCommand_exit4(t *testing.T) {
	boot := newBoot(t)
	called := false
	err := kernel.Run(boot, kernel.Inputs{Readonly: true, Mutating: true}, func(rc *kernel.RunContext) (output.Renderable, error) {
		called = true
		return nil, nil
	})
	if err == nil {
		t.Fatal("mutating command under GPLAY_READONLY = nil, want exit 4")
	}
	if got := exit.For(err); got != 4 {
		t.Errorf("exit.For = %d, want 4 (policy); err=%v", got, err)
	}
	if !strings.Contains(err.Error(), kernel.EnvReadonly) {
		t.Errorf("refusal message should name %s; got %q", kernel.EnvReadonly, err.Error())
	}
	if called {
		t.Error("business fn ran — refusal must happen BEFORE fn (no credential resolution, no network)")
	}
}

func TestRun_readonly_jsonEnvelope_exitCode4(t *testing.T) {
	boot := newBoot(t)
	var stdout bytes.Buffer
	boot.Stdout = &stdout
	_ = kernel.Run(boot, kernel.Inputs{Format: output.FormatJSON, Readonly: true, Mutating: true}, func(rc *kernel.RunContext) (output.Renderable, error) {
		return nil, nil
	})
	env := decodeOneEnvelope(t, stdout.String())
	if env.Error.ExitCode != 4 {
		t.Errorf("envelope exitCode = %d, want 4", env.Error.ExitCode)
	}
}

func TestRun_readonly_allowsDryRunOfMutatingCommand(t *testing.T) {
	boot := newBoot(t)
	called := false
	err := kernel.Run(boot, kernel.Inputs{Readonly: true, Mutating: true, DryRun: true}, func(rc *kernel.RunContext) (output.Renderable, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("--dry-run of a mutating command under GPLAY_READONLY = %v, want allowed", err)
	}
	if !called {
		t.Error("--dry-run must still run the business fn (it never writes)")
	}
}

func TestRun_readonly_allowsReadCommand(t *testing.T) {
	boot := newBoot(t)
	called := false
	err := kernel.Run(boot, kernel.Inputs{Readonly: true, Mutating: false}, func(rc *kernel.RunContext) (output.Renderable, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("read command under GPLAY_READONLY = %v, want allowed", err)
	}
	if !called {
		t.Error("a read command must still run under GPLAY_READONLY")
	}
}

func TestRun_notReadonly_runsMutatingCommand(t *testing.T) {
	boot := newBoot(t)
	called := false
	err := kernel.Run(boot, kernel.Inputs{Readonly: false, Mutating: true}, func(rc *kernel.RunContext) (output.Renderable, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("mutating command without GPLAY_READONLY = %v, want allowed", err)
	}
	if !called {
		t.Error("without the policy a mutating command must run normally")
	}
}

// TestRunCobra_readonly_marksAndEnforces wires the whole chain: a cobra command
// declared mutating (MarkMutating) + GPLAY_READONLY=1 in the env is refused
// (exit 4) without reaching the business fn — proving the annotation and env
// reading in FromCobra connect to the Run-time enforcement.
func TestRunCobra_readonly_marksAndEnforces(t *testing.T) {
	t.Setenv(kernel.EnvReadonly, "1")
	boot := newBoot(t)
	cmd := kernel.MarkMutating(&cobra.Command{Use: "write"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	called := false
	err := kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
		called = true
		return nil, nil
	})
	if got := exit.For(err); got != 4 {
		t.Fatalf("exit.For = %d, want 4; err=%v", got, err)
	}
	if called {
		t.Error("MarkMutating + GPLAY_READONLY must refuse before the fn runs")
	}
}

func TestRunCobra_readonly_unmarkedReadCommandRuns(t *testing.T) {
	t.Setenv(kernel.EnvReadonly, "1")
	boot := newBoot(t)
	cmd := &cobra.Command{Use: "read"} // NOT marked mutating
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	called := false
	if err := kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
		called = true
		return nil, nil
	}); err != nil {
		t.Fatalf("unmarked read command under GPLAY_READONLY = %v, want allowed", err)
	}
	if !called {
		t.Error("an unmarked (read) command must run under GPLAY_READONLY")
	}
}

// ---- JSON error envelope on failure (#210 / ADR-0023) ----
//
// These drive the production kernel.Run path and assert the external contract:
// under --output json a failure emits exactly one {"error":{...}} object on
// stdout carrying the semantic exit code, message, and (when present) the
// upstream reasons[] / safety requires[] — while exit codes and the table /
// markdown / success paths are untouched.

type envelopeShape struct {
	Error struct {
		ExitCode int      `json:"exitCode"`
		Message  string   `json:"message"`
		Reasons  []string `json:"reasons"`
		Requires []string `json:"requires"`
	} `json:"error"`
}

// runWithErr runs fn (returning err) through kernel.Run under the given format
// and returns the captured stdout plus the error Run surfaced.
func runWithErr(t *testing.T, format output.Format, fn func(rc *kernel.RunContext) (output.Renderable, error)) (string, error) {
	t.Helper()
	boot := newBoot(t)
	var stdout bytes.Buffer
	boot.Stdout = &stdout
	err := kernel.Run(boot, kernel.Inputs{Format: format}, fn)
	return stdout.String(), err
}

func decodeOneEnvelope(t *testing.T, stdout string) envelopeShape {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var env envelopeShape
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a well-formed JSON envelope: %v\nstdout=%q", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout carried more than one JSON value:\n%s", stdout)
	}
	return env
}

func TestRun_jsonFailure_apiError_writesEnvelopeWithReasons(t *testing.T) {
	apiErr := &api.Error{Operation: "edits.commit", Package: "com.example.app", StatusCode: 409, Message: "conflict", Reasons: []string{"editAlreadyExists"}}
	stdout, err := runWithErr(t, output.FormatJSON, func(rc *kernel.RunContext) (output.Renderable, error) {
		return nil, apiErr
	})
	if got := exit.For(err); got != 60 {
		t.Errorf("exit code = %d, want 60 (unchanged); err=%v", got, err)
	}
	env := decodeOneEnvelope(t, stdout)
	if env.Error.ExitCode != 60 {
		t.Errorf("envelope exitCode = %d, want 60", env.Error.ExitCode)
	}
	if len(env.Error.Reasons) != 1 || env.Error.Reasons[0] != "editAlreadyExists" {
		t.Errorf("envelope reasons = %v, want [editAlreadyExists]", env.Error.Reasons)
	}
}

func TestRun_jsonFailure_safetyRefusal_writesEnvelopeWithRequires(t *testing.T) {
	stdout, err := runWithErr(t, output.FormatJSON, func(rc *kernel.RunContext) (output.Renderable, error) {
		return nil, exit.SafetyFlag("confirm", "this is destructive; pass --confirm")
	})
	if got := exit.For(err); got != 3 {
		t.Errorf("exit code = %d, want 3 (unchanged)", got)
	}
	env := decodeOneEnvelope(t, stdout)
	if env.Error.ExitCode != 3 {
		t.Errorf("envelope exitCode = %d, want 3", env.Error.ExitCode)
	}
	if len(env.Error.Requires) != 1 || env.Error.Requires[0] != "confirm" {
		t.Errorf("envelope requires = %v, want [confirm]", env.Error.Requires)
	}
}

func TestRun_jsonFailure_usageError_writesEnvelope(t *testing.T) {
	stdout, err := runWithErr(t, output.FormatJSON, func(rc *kernel.RunContext) (output.Renderable, error) {
		return nil, exit.Usagef("missing --to")
	})
	if got := exit.For(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	env := decodeOneEnvelope(t, stdout)
	if env.Error.ExitCode != 2 {
		t.Errorf("envelope exitCode = %d, want 2", env.Error.ExitCode)
	}
	if env.Error.Message == "" {
		t.Errorf("envelope message should be present")
	}
}

func TestRun_tableFailure_noEnvelope(t *testing.T) {
	stdout, err := runWithErr(t, output.FormatTable, func(rc *kernel.RunContext) (output.Renderable, error) {
		return nil, exit.Usagef("missing --to")
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if stdout != "" {
		t.Errorf("table-format failure must leave stdout empty; got %q", stdout)
	}
}

// TestRun_jsonFailure_selfRendered_noDoubleEnvelope guards the doctor case: a
// command that writes its own output to stdout then returns an error must NOT
// get an envelope appended (which would yield two JSON objects on stdout).
func TestRun_jsonFailure_selfRendered_noDoubleEnvelope(t *testing.T) {
	stdout, _ := runWithErr(t, output.FormatJSON, func(rc *kernel.RunContext) (output.Renderable, error) {
		// Stand in for doctor: render a JSON checklist, then fail.
		if _, werr := io.WriteString(rc.Stdout, `{"checks":[{"ok":false}]}`+"\n"); werr != nil {
			t.Fatalf("write: %v", werr)
		}
		return nil, exit.Usagef("a check failed")
	})
	// Exactly one JSON value — the command's own — survives; no appended envelope.
	dec := json.NewDecoder(strings.NewReader(stdout))
	var first map[string]any
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout)
	}
	if _, isEnvelope := first["error"]; isEnvelope {
		t.Errorf("self-rendered output was replaced by an envelope:\n%s", stdout)
	}
	if dec.More() {
		t.Errorf("an envelope was appended after the command's own output:\n%s", stdout)
	}
}

func TestRun_jsonSuccess_noEnvelope(t *testing.T) {
	boot := newBoot(t)
	var stdout bytes.Buffer
	boot.Stdout = &stdout
	if err := kernel.Run(boot, kernel.Inputs{Format: output.FormatJSON}, func(rc *kernel.RunContext) (output.Renderable, error) {
		return fakePayload{msg: "ok"}, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stdout.String(), `"error"`) {
		t.Errorf("success path must not emit an error envelope; got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"msg":"ok"`) {
		t.Errorf("success payload missing; got %q", stdout.String())
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

// signedAccount returns a ServiceAccount backed by a freshly generated RSA key,
// so AuthedClient / UploadClient can build a real OAuth2 token source (lazily —
// no network) and the returned client's deadline can be inspected.
func signedAccount(t *testing.T) *serviceaccount.ServiceAccount {
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
		"client_email": "ci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sa, err := serviceaccount.Parse(raw)
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	return sa
}

// TestAuthedClient_defaultControlPlaneTimeout: with no --timeout, a
// control-plane client carries the 60s default deadline (#209).
func TestAuthedClient_defaultControlPlaneTimeout(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{})
	rc.Account = signedAccount(t)
	hc, err := rc.AuthedClient()
	if err != nil {
		t.Fatalf("AuthedClient: %v", err)
	}
	if hc.Timeout != 60*time.Second {
		t.Errorf("AuthedClient default timeout = %v, want 60s", hc.Timeout)
	}
}

// TestAuthedClient_timeoutOverride: --timeout overrides the control-plane
// default on the returned client.
func TestAuthedClient_timeoutOverride(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{Timeout: 5 * time.Second})
	rc.Account = signedAccount(t)
	hc, err := rc.AuthedClient()
	if err != nil {
		t.Fatalf("AuthedClient: %v", err)
	}
	if hc.Timeout != 5*time.Second {
		t.Errorf("AuthedClient with --timeout=5s = %v, want 5s", hc.Timeout)
	}
}

// TestAuthedClient_retry_movesDeadlineToMiddleware: with --retry, the
// per-request deadline moves off the client onto the retry middleware (which
// applies it per attempt), so client.Timeout is 0. Without --retry it stays on
// the client (asserted above).
func TestAuthedClient_retry_movesDeadlineToMiddleware(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{Timeout: 5 * time.Second, Retry: 2})
	rc.Account = signedAccount(t)
	hc, err := rc.AuthedClient()
	if err != nil {
		t.Fatalf("AuthedClient: %v", err)
	}
	if hc.Timeout != 0 {
		t.Errorf("with --retry the deadline is per-attempt in the middleware; client.Timeout = %v, want 0", hc.Timeout)
	}
}

// TestUploadClient_exemptFromDefault: a media-upload client has NO deadline
// when --timeout is unset, so a large transfer is never killed by the short
// control-plane default.
func TestUploadClient_exemptFromDefault(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{})
	rc.Account = signedAccount(t)
	hc, err := rc.UploadClient()
	if err != nil {
		t.Fatalf("UploadClient: %v", err)
	}
	if hc.Timeout != 0 {
		t.Errorf("UploadClient default timeout = %v, want 0 (unbounded)", hc.Timeout)
	}
}

// TestUploadClient_honorsExplicitTimeout: an explicit --timeout DOES bound an
// upload client (the override applies to every request, uploads included).
func TestUploadClient_honorsExplicitTimeout(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{Timeout: 7 * time.Second})
	rc.Account = signedAccount(t)
	hc, err := rc.UploadClient()
	if err != nil {
		t.Fatalf("UploadClient: %v", err)
	}
	if hc.Timeout != 7*time.Second {
		t.Errorf("UploadClient with --timeout=7s = %v, want 7s", hc.Timeout)
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

// TestConfirmf_writesCheckmarkLineToStderr asserts rc.Confirmf prepends the
// ✓ marker, appends a newline, formats its args, and writes the result to
// stderr only — never stdout. Per DESIGN §8 the success line is a stderr log
// line that must survive --output json (where stdout is verbatim API data).
func TestConfirmf_writesCheckmarkLineToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(),
		kernel.Boot{Stdout: &stdout, Stderr: &stderr}, kernel.Inputs{})

	rc.Confirmf("uploaded versionCode %d to track %q", 42, "internal")

	if got, want := stderr.String(), "✓ uploaded versionCode 42 to track \"internal\"\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if stdout.Len() != 0 {
		t.Errorf("Confirmf wrote %q to stdout; the ✓ line must go to stderr only", stdout.String())
	}
}

// TestConfirmf_nilStderrIsSafe asserts Confirmf is a no-op (no panic) when a
// hand-built RunContext leaves Stderr nil — the kernel paths always wire it,
// but the helper must not assume so.
func TestConfirmf_nilStderrIsSafe(t *testing.T) {
	rc := &kernel.RunContext{} // Stderr deliberately nil
	rc.Confirmf("should not panic")
}
