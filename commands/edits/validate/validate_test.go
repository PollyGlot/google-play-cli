package validate_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	validatecmd "github.com/PollyGlot/google-play-cli/commands/edits/validate"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const pkg = "com.example.app"

// newValidateFake serves the edits.validate POST; validateStatus (0 → 200)
// forces a non-2xx for the failure test.
func newValidateFake(validateStatus int) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if c.Method != http.MethodPost || !strings.HasSuffix(c.Path, "/edits/edit-9:validate") {
			return 0, "", false
		}
		if validateStatus != 0 {
			return validateStatus, `{"error":{"code":400,"message":"The release notes exceed 500 characters"}}`, true
		}
		return 200, `{"id":"edit-9","expiryTimeSeconds":"1700000000"}`, true
	})
}

// validateCalls counts the edits.validate calls and fails the test on any
// other request: an insert, a commit or a delete is a contract breach,
// including on the paths where Run's error would otherwise hide it.
func validateCalls(t *testing.T, f *testkit.Fake) int {
	t.Helper()
	n := 0
	for _, c := range f.Calls() {
		if c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits/edit-9:validate") {
			n++
			continue
		}
		t.Errorf("unexpected request: %s %s", c.Method, c.Path)
	}
	return n
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, string) {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	gplayDir := filepath.Join(t.TempDir(), ".gplay")
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Resolved = &config.Resolved{Pin: pkg, ProjectSharedPath: filepath.Join(gplayDir, "config.json")}
	return rc, gplayDir
}

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v (%T) has no ExitCode", err, err)
	}
	return c.ExitCode()
}

func render(t *testing.T, r output.Renderable, f output.Format) string {
	t.Helper()
	var buf bytes.Buffer
	if err := output.Render(&buf, f, r.Renderers()); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestRun_valid_mirrorsAPIBodyAndKeepsPin(t *testing.T) {
	fake := newValidateFake(0)
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	r, err := validatecmd.Run(rc, validatecmd.Input{Package: pkg})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := validateCalls(t, fake); n != 1 {
		t.Errorf("validateCalls = %d, want 1", n)
	}
	// ADR-0003: --output json is the API body, not a gplay envelope.
	if got, want := strings.TrimSpace(render(t, r, output.FormatJSON)), `{"id":"edit-9","expiryTimeSeconds":"1700000000"}`; got != want {
		t.Errorf("json = %q, want the verbatim AppEdit %q", got, want)
	}
	if out := render(t, r, output.FormatTable); !strings.Contains(out, "validated explicit edit edit-9") {
		t.Errorf("table = %q", out)
	}
	// Validate never commits: the Edit is still open, so the pin must survive.
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); !ok {
		t.Error("pin cleared by validate; it must stay for the later commit/discard")
	}
}

func TestRun_invalid_400_exit30_keepsPin(t *testing.T) {
	fake := newValidateFake(400)
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	_, err := validatecmd.Run(rc, validatecmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 30 {
		t.Fatalf("exit = %d, want 30 (API 4xx); err = %v", code, err)
	}
	if !strings.Contains(err.Error(), "release notes exceed") {
		t.Errorf("err = %q, want the API's message surfaced", err)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); !ok {
		t.Error("a failed validate must leave the pin in place for a fix-and-retry")
	}
	validateCalls(t, fake)
}

func TestRun_noOpenEdit_exit60_noNetwork(t *testing.T) {
	fake := newValidateFake(0)
	rc, _ := newRC(t, fake)

	_, err := validatecmd.Run(rc, validatecmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 60 {
		t.Fatalf("exit = %d, want 60 (no open edit)", code)
	}
	if n := validateCalls(t, fake); n != 0 {
		t.Errorf("no open edit must fail before the network; validateCalls = %d", n)
	}
}
