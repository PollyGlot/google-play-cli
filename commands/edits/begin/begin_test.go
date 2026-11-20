package begin_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	begincmd "github.com/PollyGlot/google-play-cli/commands/edits/begin"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const pkg = "com.example.app"

func newBeginFake(editID string) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, `{"id":"` + editID + `","expiryTimeSeconds":"1700000000"}`, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		}
		return 0, "", false
	})
}

func countCalls(t *testing.T, f *testkit.Fake) (inserts, deletes int) {
	t.Helper()
	for _, c := range f.Calls() {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			inserts++
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			deletes++
		default:
			t.Errorf("unexpected request: %s %s", c.Method, c.Path)
		}
	}
	return inserts, deletes
}

type failWriteFS struct{ config.FS }

func (failWriteFS) WriteFile(string, []byte, fs.FileMode) error {
	return errors.New("disk full")
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

func TestRun_opensEditAndWritesPin(t *testing.T) {
	fake := newBeginFake("edit-42")
	rc, gplayDir := newRC(t, fake)

	r, err := begincmd.Run(rc, begincmd.Input{Package: pkg})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if inserts, _ := countCalls(t, fake); inserts != 1 {
		t.Errorf("insertCalls = %d, want 1", inserts)
	}
	pin, ok, err := editpin.Lookup(config.OSFS{}, gplayDir, pkg)
	if err != nil || !ok {
		t.Fatalf("pin not written: ok=%v err=%v", ok, err)
	}
	if pin.EditID != "edit-42" {
		t.Errorf("pinned editId = %q, want edit-42", pin.EditID)
	}
	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatJSON, r.Renderers()); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), `"editId": "edit-42"`) || !strings.Contains(buf.String(), `"open": true`) {
		t.Errorf("json payload = %s", buf.String())
	}
}

func TestRun_alreadyOpen_exit60_noNetwork(t *testing.T) {
	fake := newBeginFake("edit-new")
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-already"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	_, err := begincmd.Run(rc, begincmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 60 {
		t.Fatalf("exit = %d, want 60 (already open)", code)
	}
	if inserts, _ := countCalls(t, fake); inserts != 0 {
		t.Errorf("a second begin must not open another Edit; insertCalls = %d", inserts)
	}
}

func TestRun_pinWriteFailure_discardsServerEdit(t *testing.T) {
	fake := newBeginFake("edit-orphan")
	rc, gplayDir := newRC(t, fake)
	rc.FS = failWriteFS{config.OSFS{}}

	_, err := begincmd.Run(rc, begincmd.Input{Package: pkg})
	if err == nil {
		t.Fatal("expected the pin-write failure to surface")
	}
	inserts, deletes := countCalls(t, fake)
	if inserts != 1 {
		t.Errorf("insertCalls = %d, want 1 (the Edit was opened)", inserts)
	}
	if deletes != 1 {
		t.Errorf("deleteCalls = %d, want 1 (the opened Edit must be discarded on pin-write failure)", deletes)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("a failed begin must leave no pin behind")
	}
}

func TestRun_noProject_exit2(t *testing.T) {
	fake := newBeginFake("edit-x")
	rc, _ := newRC(t, fake)
	rc.Resolved = &config.Resolved{Pin: pkg}

	_, err := begincmd.Run(rc, begincmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 2 {
		t.Fatalf("exit = %d, want 2 (no project)", code)
	}
	if inserts, _ := countCalls(t, fake); inserts != 0 {
		t.Errorf("no project must fail before the network; insertCalls = %d", inserts)
	}
}
