package set_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	setcmd "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/set"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// newEFFake serves the implicit Edit around expansionfiles.update: insert,
// PUT, commit.
func newEFFake() *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, `{"id":"edit1"}`, true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/expansionFiles/"):
			return http.StatusOK, `{"referencesVersion":140}`, true
		case strings.HasSuffix(c.Path, ":commit"):
			return http.StatusOK, `{"id":"edit1"}`, true
		}
		return 0, "", false
	})
}

// apiCalls lists the recorded API calls as "METHOD path".
func apiCalls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

// putBody returns the body of the recorded PUT, nil when none was sent.
func putBody(f *testkit.Fake) []byte {
	for _, c := range f.Calls() {
		if c.Method == http.MethodPut {
			return c.Body
		}
	}
	return nil
}

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

// TestRun_missingReferencesVersion_exit2 asserts --references-version is required.
func TestRun_missingReferencesVersion_exit2(t *testing.T) {
	r := newEFFake()
	rc := newRC(t, r)
	_, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
	if len(r.Calls()) != 0 || r.TokenExchanges() != 0 {
		t.Errorf("must not reach the network; calls=%v, token exchanges=%d", apiCalls(r), r.TokenExchanges())
	}
}

// TestRun_happyPath_putsReference asserts insert → PUT → commit with the body.
func TestRun_happyPath_putsReference(t *testing.T) {
	r := newEFFake()
	rc := newRC(t, r)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	if _, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main", ReferencesVersion: 140}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit1/apks/142/expansionFiles/main",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit1:commit",
	}
	if got := apiCalls(r); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", got, want)
	}
	if n := r.TokenExchanges(); n != 1 {
		t.Errorf("token exchanges = %d, want 1", n)
	}
	if body := putBody(r); !strings.Contains(string(body), `"referencesVersion":140`) {
		t.Errorf("PUT body %q should set referencesVersion 140", body)
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") {
		t.Errorf("stderr missing ✓:\n%s", stderr.String())
	}
}
