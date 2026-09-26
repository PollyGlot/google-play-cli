// Package mappings_test exercises `gplay releases mappings upload` at the
// kernel level: a RunContext built by hand, a RoundTripper injected via
// the oauth2.HTTPClient context key, and Run invoked directly. The
// RoundTripper sees both the /token exchange and the androidpublisher
// calls (edits.insert, deobfuscationfiles.upload, edits.commit/delete).
package mappings_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/releases/mappings"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// newMappingTransport routes the Edit lifecycle calls a standalone mapping
// upload makes. The Fake records every call; the wrapper only adds the
// session URI (Location) to the resumable initiate, which a responder cannot
// express.
func newMappingTransport(editID string) (*testkit.Fake, http.RoundTripper) {
	f := testkit.NewFake(testkit.ReplyHeader(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return http.StatusNoContent, "", true
		case c.Method == http.MethodPost && strings.Contains(c.Path, "/deobfuscationFiles/"):
			return http.StatusOK, "", true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/deobfuscationFiles/"):
			return http.StatusOK, `{"deobfuscationFile":{"symbolType":"proguard"}}`, true
		case strings.HasSuffix(c.Path, ":commit"):
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, editID), true
		}
		return 0, "", false
	}, "Location", func(c testkit.Call, status int) string {
		// Resumable initiate: the session URI goes in Location.
		if c.Method == http.MethodPost && strings.Contains(c.Path, "/deobfuscationFiles/") {
			return "https://" + c.Host + c.Path + "?upload_id=session-" + editID
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

func writeFakeMapping(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mapping.txt")
	if err := os.WriteFile(p, []byte("com.example.Foo -> a.a:\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// TestRun_happyPath_beginUploadCommit_andConfirms asserts a standalone
// mapping upload opens its own Edit, POSTs the mapping keyed by the given
// --version-code + proguard, commits, and prints a ✓ confirmation (#250).
func TestRun_happyPath_beginUploadCommit_andConfirms(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt, transport := newMappingTransport("edit-map")
	rc := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := mappings.Run(rc, mappings.Input{
		Package:     "com.example.app",
		MappingPath: mapping,
		VersionCode: 142,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-map/apks/142/deobfuscationFiles/proguard",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-map/apks/142/deobfuscationFiles/proguard",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-map:commit",
	}
	calls := apiCalls(rt)
	if len(calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(calls), calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, calls[i], want)
		}
	}
	if n := rt.TokenExchanges(); n != 1 {
		t.Errorf("token exchanges = %d, want 1", n)
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") {
		t.Errorf("missing ✓ confirmation; stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "142") {
		t.Errorf("✓ line should name versionCode 142; stderr=%q", stderr.String())
	}
}

// TestRun_missingVersionCode_exit2_noHTTP asserts --version-code is
// required: omitting it is a CLI misuse (exit 2) before any HTTP.
func TestRun_missingVersionCode_exit2_noHTTP(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt, transport := newMappingTransport("")
	rc := newRC(t, transport)

	_, err := mappings.Run(rc, mappings.Input{
		Package:     "com.example.app",
		MappingPath: mapping,
	})
	if err == nil {
		t.Fatal("Run returned nil error with no --version-code")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("hit the network without --version-code: %v", apiCalls(rt))
	}
}

// TestRun_nativeCodeType_inPath asserts --type nativeCode lands verbatim
// in the deobfuscationFiles path segment.
func TestRun_nativeCodeType_inPath(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt, transport := newMappingTransport("edit-map")
	rc := newRC(t, transport)

	if _, err := mappings.Run(rc, mappings.Input{
		Package:     "com.example.app",
		MappingPath: mapping,
		VersionCode: 7,
		Type:        "nativeCode",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, c := range apiCalls(rt) {
		if strings.HasSuffix(c, "/apks/7/deobfuscationFiles/nativeCode") {
			found = true
		}
	}
	if !found {
		t.Errorf("no deobfuscationFiles/nativeCode call in %v", apiCalls(rt))
	}
}

// TestRun_dryRun_noConfirmation_noHTTP asserts --dry-run validates the
// file and emits no ✓ and no network calls (not even the /token exchange).
func TestRun_dryRun_noConfirmation_noHTTP(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt, transport := newMappingTransport("")
	rc := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := mappings.Run(rc, mappings.Input{
		Package:     "com.example.app",
		MappingPath: mapping,
		VersionCode: 142,
		DryRun:      true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓; stderr=%q", stderr.String())
	}
	if touched(rt) {
		t.Errorf("dry-run hit the network: %v", apiCalls(rt))
	}
}

// TestNewCommand_registersExpectedFlags is a thin smoke test for the
// cobra wiring.
func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := mappings.NewCommand(kernel.Boot{})
	for _, name := range []string{
		"package",
		"version-code",
		"type",
		"keep-edit-on-failure",
		"dry-run",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "upload <mapping.txt>" {
		t.Errorf("cmd.Use = %q, want %q", got, "upload <mapping.txt>")
	}
}
