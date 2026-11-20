package discard_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	discardcmd "github.com/PollyGlot/google-play-cli/commands/edits/discard"
	"github.com/PollyGlot/google-play-cli/commands/edits/editscmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const pkg = "com.example.app"

func newDiscardFake(deleteStatus int) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if c.Method != http.MethodDelete || !strings.Contains(c.Path, "/edits/") {
			return 0, "", false
		}
		if deleteStatus != 0 {
			return deleteStatus, `{"error":{"code":404,"message":"edit not found"}}`, true
		}
		return 204, "", true
	})
}

func deleteCalls(f *testkit.Fake) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Method == http.MethodDelete {
			n++
		}
	}
	return n
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, string) {
	t.Helper()
	rc, gplayDir, _ := newRCWithStderr(t, rt)
	return rc, gplayDir
}

func newRCWithStderr(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, string, *bytes.Buffer) {
	t.Helper()
	stderr := &bytes.Buffer{}
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	gplayDir := filepath.Join(t.TempDir(), ".gplay")
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Resolved = &config.Resolved{Pin: pkg, ProjectSharedPath: filepath.Join(gplayDir, "config.json")}
	return rc, gplayDir, stderr
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

func TestRun_discardsAndClearsPin(t *testing.T) {
	fake := newDiscardFake(0)
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-3"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	if _, err := discardcmd.Run(rc, discardcmd.Input{Package: pkg}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if deleteCalls(fake) != 1 {
		t.Errorf("deleteCalls = %d, want 1", deleteCalls(fake))
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("pin still present after discard")
	}
}

func TestRun_discard404_isSuccessAndClearsPin(t *testing.T) {
	fake := newDiscardFake(404)
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-gone"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	if _, err := discardcmd.Run(rc, discardcmd.Input{Package: pkg}); err != nil {
		t.Fatalf("a 404 discard should succeed: %v", err)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("pin must be cleared even when the Edit was already gone")
	}
}

func TestRun_discardServerError_clearsPinButReturnsError(t *testing.T) {
	fake := newDiscardFake(500)
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-stuck"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	_, err := discardcmd.Run(rc, discardcmd.Input{Package: pkg})
	if err == nil {
		t.Fatal("a non-404 remote discard error must surface")
	}
	if calls := fake.Calls(); len(calls) != 1 || deleteCalls(fake) != 1 {
		t.Errorf("calls = %+v, want the single DELETE: the remote discard must be attempted before the pin is cleared", calls)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("the pin must be cleared even when the remote discard failed")
	}
}

func TestRun_unreadablePin_removedWithoutNetwork(t *testing.T) {
	fake := newDiscardFake(0)
	rc, gplayDir, stderrBuf := newRCWithStderr(t, fake)
	if err := (config.OSFS{}).MkdirAll(gplayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (config.OSFS{}).WriteFile(editpin.Path(gplayDir, pkg), []byte(`{"editId": `), 0o644); err != nil {
		t.Fatalf("seed corrupt pin: %v", err)
	}

	got, err := discardcmd.Run(rc, discardcmd.Input{Package: pkg})
	if err != nil {
		t.Fatalf("Run on an unreadable pin: %v", err)
	}
	if n := len(fake.Calls()); n != 0 {
		t.Errorf("calls = %d, want 0: an unreadable pin names no Edit to discard", n)
	}
	if _, err := (config.OSFS{}).Stat(editpin.Path(gplayDir, pkg)); err == nil {
		t.Error("unreadable pin still on disk after discard")
	}
	if p, ok := got.(editscmd.Payload); !ok || p.Action != editscmd.ActionClearedUnreadable || p.Open {
		t.Errorf("payload = %#v, want action %q, open false", got, editscmd.ActionClearedUnreadable)
	}
	stderr := stderrBuf.String()
	if !strings.Contains(stderr, "removed unreadable pin") || !strings.Contains(stderr, "nothing was sent to Play") {
		t.Errorf("stderr %q does not say the pin was removed without an API call", stderr)
	}
}

func TestRun_pinThatCannotBeRead_isNotRemoved(t *testing.T) {
	fake := newDiscardFake(0)
	rc, gplayDir := newRC(t, fake)
	if err := (config.OSFS{}).MkdirAll(editpin.Path(gplayDir, pkg), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := discardcmd.Run(rc, discardcmd.Input{Package: pkg}); err == nil {
		t.Fatal("a pin path that cannot be read must fail, not be cleared")
	}
	if _, err := (config.OSFS{}).Stat(editpin.Path(gplayDir, pkg)); err != nil {
		t.Errorf("the unread pin path was removed: %v", err)
	}
}

func TestRun_noOpenEdit_exit60_noNetwork(t *testing.T) {
	fake := newDiscardFake(0)
	rc, _ := newRC(t, fake)

	_, err := discardcmd.Run(rc, discardcmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 60 {
		t.Fatalf("exit = %d, want 60 (no open edit)", code)
	}
	if deleteCalls(fake) != 0 {
		t.Errorf("no open edit must fail before the network; deleteCalls = %d", deleteCalls(fake))
	}
}
