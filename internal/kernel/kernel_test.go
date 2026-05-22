package kernel_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

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
	be, _, err := keystore.Select(keystore.SelectOptions{
		Keyring: boot.Keyring, FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if err := be.Save("ci", []byte(fakeSAJSON)); err != nil {
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
		if rc.Account == nil {
			t.Fatal("rc.Account is nil after seeded active account")
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
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c := strings.Count(stderr.String(), "keystore: using"); c != 1 {
		t.Errorf("verbose: backend label logged %d times, want 1; stderr=%q", c, stderr.String())
	}
}
