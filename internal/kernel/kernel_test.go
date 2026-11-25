package kernel_test

import (
	"bytes"
	"context"
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
	"github.com/zalando/go-keyring"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

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

func TestGroup_carriesGroupingDefaults(t *testing.T) {
	a := &cobra.Command{Use: "a", Run: func(*cobra.Command, []string) {}}
	b := &cobra.Command{Use: "b", Run: func(*cobra.Command, []string) {}}
	g := kernel.Group("grp", "a grouping noun", a, b)

	if g.Use != "grp" || g.Short != "a grouping noun" {
		t.Errorf("Use/Short = %q/%q, want grp/a grouping noun", g.Use, g.Short)
	}
	if !g.SilenceUsage || !g.SilenceErrors {
		t.Errorf("SilenceUsage/SilenceErrors = %v/%v, want true/true", g.SilenceUsage, g.SilenceErrors)
	}
	if g.RunE == nil {
		t.Fatal("RunE is nil: a bare group would print help on an unknown subcommand")
	}
	if err := g.RunE(g, []string{"nonesuch"}); exit.For(err) != 2 {
		t.Errorf("RunE(unknown) exit = %d, want 2 (GroupRunE); err=%v", exit.For(err), err)
	}
	if got := g.Commands(); len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("children = %v, want [a b]", got)
	}
	if a.Parent() != g {
		t.Error("child not parented to the group")
	}
}

const fakeSAJSON = `{
  "type": "service_account",
  "project_id": "p",
  "private_key": "-----BEGIN PRIVATE KEY-----\nXX\n-----END PRIVATE KEY-----\n",
  "client_email": "ci@p.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

type unavailableKeyring struct{}

func (unavailableKeyring) Set(string, string, string) error { return errBoom }
func (unavailableKeyring) Get(string, string) (string, error) {
	return "", errBoom
}
func (unavailableKeyring) Delete(string, string) error { return errBoom }

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
		return "", keyring.ErrNotFound
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
	if rc.Stderr == nil {
		t.Fatalf("Stderr not wired from Boot")
	}
	if _, err := io.WriteString(rc.Stderr, "wired\n"); err != nil {
		t.Fatalf("write to rc.Stderr: %v", err)
	}
	if got := boot.Stderr.(*bytes.Buffer).String(); !strings.Contains(got, "wired") {
		t.Errorf("rc.Stderr does not forward to Boot.Stderr, got %q", got)
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

func TestRun_verbose_preAuthIsProbeFree(t *testing.T) {
	boot := newBoot(t)
	spy := &countingKeyring{}
	boot.Keyring = spy
	var stderr bytes.Buffer
	boot.Stderr = &stderr
	if err := kernel.Run(boot, kernel.Inputs{Verbose: true}, func(rc *kernel.RunContext) (output.Renderable, error) {
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

func TestRun_authNeedingCommand_probesKeyring(t *testing.T) {
	boot := newBoot(t)
	spy := newAvailableKeyring()
	boot.Keyring = spy
	seedActiveAccount(t, boot)
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
	boot := newBoot(t)
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
	seedActiveConfigOnly(t, boot)
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
	boot.Keyring = faultyGetKeyring{}
	seedActiveConfigOnly(t, boot)
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
	seedActiveAccount(t, boot)
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
		if first != second { //nolint:errorlint // pointer identity is what proves memoisation
			t.Errorf("EnsureAccount did not memoise the invalid error: first=%p second=%p", first, second)
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

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
		t.Error("business fn ran: refusal must happen BEFORE fn (no credential resolution, no network)")
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
	err := kernel.Run(boot, kernel.Inputs{Readonly: true, Mutating: true, Preview: true}, func(rc *kernel.RunContext) (output.Renderable, error) {
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

func TestRunCobra_readonly_planOfMutatingCommandRuns(t *testing.T) {
	t.Setenv(kernel.EnvReadonly, "1")
	boot := newBoot(t)
	cmd := kernel.MarkMutating(&cobra.Command{Use: "complete"})
	cmd.Flags().Bool("plan", false, "")
	if err := cmd.Flags().Set("plan", "true"); err != nil {
		t.Fatal(err)
	}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	called := false
	if err := kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
		called = true
		return nil, nil
	}); err != nil {
		t.Fatalf("--plan of a mutating command under GPLAY_READONLY = %v, want allowed", err)
	}
	if !called {
		t.Error("--plan must run the business fn under GPLAY_READONLY")
	}
}

func TestRunCobra_readonly_unmarkedReadCommandRuns(t *testing.T) {
	t.Setenv(kernel.EnvReadonly, "1")
	boot := newBoot(t)
	cmd := &cobra.Command{Use: "read"}
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

type envelopeShape struct {
	Error struct {
		Code      string   `json:"code"`
		ExitCode  int      `json:"exitCode"`
		Retryable bool     `json:"retryable"`
		Operation string   `json:"operation"`
		Package   string   `json:"package"`
		Message   string   `json:"message"`
		Reasons   []string `json:"reasons"`
		Requires  []string `json:"requires"`
	} `json:"error"`
}

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
	if env.Error.Code != string(exit.CodeEditAlreadyExists) {
		t.Errorf("envelope code = %q, want %q (PRD #447)", env.Error.Code, exit.CodeEditAlreadyExists)
	}
	if env.Error.Retryable {
		t.Error("envelope marks an already-open Edit retryable; it is not")
	}
	if env.Error.Operation != "edits.commit" || env.Error.Package != "com.example.app" {
		t.Errorf("envelope operation/package = %q/%q, want edits.commit/com.example.app",
			env.Error.Operation, env.Error.Package)
	}
}

func TestRun_jsonFailure_stderrCarriesNoEnvelope(t *testing.T) {
	boot := newBoot(t)
	var stdout, stderr bytes.Buffer
	boot.Stdout = &stdout
	boot.Stderr = &stderr
	apiErr := &api.Error{Operation: "edits.insert", Package: "com.example.app", StatusCode: 429, Message: "too many requests"}
	err := kernel.Run(boot, kernel.Inputs{Format: output.FormatJSON}, func(rc *kernel.RunContext) (output.Renderable, error) {
		return nil, apiErr
	})
	if err == nil {
		t.Fatal("expected the failure to surface")
	}
	env := decodeOneEnvelope(t, stdout.String())
	if env.Error.Code != string(exit.CodeRateLimitExceeded) || !env.Error.Retryable {
		t.Errorf("envelope = %+v, want RATE_LIMIT_EXCEEDED and retryable", env.Error)
	}
	if strings.Contains(stderr.String(), `"code"`) {
		t.Errorf("stderr must stay the human channel; got %q", stderr.String())
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

func TestRun_jsonFailure_selfRendered_noDoubleEnvelope(t *testing.T) {
	stdout, _ := runWithErr(t, output.FormatJSON, func(rc *kernel.RunContext) (output.Renderable, error) {
		if _, werr := io.WriteString(rc.Stdout, `{"checks":[{"ok":false}]}`+"\n"); werr != nil {
			t.Fatalf("write: %v", werr)
		}
		return nil, exit.Usagef("a check failed")
	})
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

func signedAccount(t *testing.T) *serviceaccount.ServiceAccount {
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
		if err.Error() != kernel.NoAccountError().Error() {
			t.Errorf("absent AuthedClient = %q, want the shared NoAccountError wording", err.Error())
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestNoAccountError_namesEveryFix(t *testing.T) {
	err := kernel.NoAccountError()
	if code := exit.For(err); code != 10 {
		t.Errorf("exit.For = %d, want 10", code)
	}
	for _, want := range []string{"no Account resolved", "gplay auth login", "--account", "GPLAY_ACCOUNT", "--service-account", "GPLAY_SERVICE_ACCOUNT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("NoAccountError() = %q, want it to name %q", err, want)
		}
	}
}

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

func TestConfirmf_nilStderrIsSafe(t *testing.T) {
	rc := &kernel.RunContext{}
	rc.Confirmf("should not panic")
}
