package upload_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	uploadcmd "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/upload"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// newEFTransport serves the implicit Edit around a resumable expansion-file
// upload: insert, initiate, chunk PUT, commit. The Fake records every call;
// the wrapper only adds the session URI (Location) to the initiate response,
// which a responder cannot express.
func newEFTransport() (*testkit.Fake, http.RoundTripper) {
	f := testkit.NewFake(testkit.ReplyHeader(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, `{"id":"edit1","expiryTimeSeconds":"1700000000"}`, true
		case c.Method == http.MethodPost && strings.Contains(c.Path, "/expansionFiles/"):
			return http.StatusOK, "", true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/expansionFiles/"):
			return http.StatusOK, `{"expansionFile":{"fileSize":"123"}}`, true
		case strings.HasSuffix(c.Path, ":commit"):
			return http.StatusOK, `{"id":"edit1"}`, true
		case c.Method == http.MethodDelete:
			return http.StatusNoContent, "", true
		}
		return 0, "", false
	}, "Location", func(c testkit.Call, status int) string {
		// Resumable initiate: the session URI goes in Location.
		if c.Method == http.MethodPost && strings.Contains(c.Path, "/expansionFiles/") {
			return "https://" + c.Host + c.Path + "?upload_id=session-1"
		}
		return ""
	}))
	return f, f
}

// apiCalls lists the recorded API calls as "METHOD path".
func apiCalls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

// touched reports whether anything reached the transport, token exchange
// included.
func touched(f *testkit.Fake) bool { return len(f.Calls()) != 0 || f.TokenExchanges() != 0 }

func newRC(t *testing.T, transport http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: transport})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

func writeOBB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "assets.obb")
	if err := os.WriteFile(p, []byte("fake obb"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err = %v (%T), want ExitCode()", err, err)
	}
	return c.ExitCode()
}

// TestRun_happyPath_editSequence asserts insert → upload → commit and a verbatim
// pass-through, with a ✓.
func TestRun_happyPath_editSequence(t *testing.T) {
	r, transport := newEFTransport()
	rc := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	res, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main", OBBPath: writeOBB(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit1/apks/142/expansionFiles/main",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit1/apks/142/expansionFiles/main",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit1:commit",
	}
	calls := apiCalls(r)
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}
	if n := r.TokenExchanges(); n != 1 {
		t.Errorf("token exchanges = %d, want 1", n)
	}
	var out bytes.Buffer
	if err := res.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"fileSize":"123"`) {
		t.Errorf("json %s should pass the upload response through", out.String())
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") {
		t.Errorf("stderr missing ✓:\n%s", stderr.String())
	}
}

// TestRun_missingVersionCode_exit2_noNetwork.
func TestRun_missingVersionCode_exit2_noNetwork(t *testing.T) {
	r, transport := newEFTransport()
	rc := newRC(t, transport)
	_, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", Type: "main", OBBPath: writeOBB(t)})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if touched(r) {
		t.Errorf("must not reach the network; calls=%v", apiCalls(r))
	}
}

// TestRun_badType_exit2_noNetwork.
func TestRun_badType_exit2_noNetwork(t *testing.T) {
	r, transport := newEFTransport()
	rc := newRC(t, transport)
	_, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "nope", OBBPath: writeOBB(t)})
	if got := exitOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
	if touched(r) {
		t.Errorf("must not reach the network; calls=%v", apiCalls(r))
	}
}

// TestRun_dryRun_validatesFile_noNetwork: a directory path fails offline (exit
// 20); a good file rehearses with no network and no ✓.
func TestRun_dryRun_validatesFile_noNetwork(t *testing.T) {
	r, transport := newEFTransport()
	rc := newRC(t, transport)
	// directory → exit 20
	if got := exitOf(t, mustErr(uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main", OBBPath: t.TempDir(), DryRun: true}))); got != 20 {
		t.Errorf("directory dry-run exit = %d, want 20", got)
	}
	if touched(r) {
		t.Errorf("dry-run must make no network call; calls=%v", apiCalls(r))
	}
	// good file → rehearses, no network
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main", OBBPath: writeOBB(t), DryRun: true}); err != nil {
		t.Fatalf("dry-run good file: %v", err)
	}
	if touched(r) {
		t.Errorf("dry-run must make no network call; calls=%v", apiCalls(r))
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓; stderr=%q", stderr.String())
	}
}

func mustErr(_ output.Renderable, err error) error { return err }
