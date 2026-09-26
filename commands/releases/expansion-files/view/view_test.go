package view_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	viewcmd "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/view"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// newEFFake serves the read-only Edit around expansionfiles.get: insert,
// GET, discard.
func newEFFake() *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, `{"id":"edit1"}`, true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/expansionFiles/"):
			return http.StatusOK, `{"referencesVersion":140}`, true
		case c.Method == http.MethodDelete:
			return http.StatusNoContent, "", true
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

// TestRun_readOnlyEditSequence asserts insert → get → discard and the parsed
// referencesVersion surfaces in the table while JSON is verbatim.
func TestRun_readOnlyEditSequence(t *testing.T) {
	r := newEFFake()
	rc := newRC(t, r)
	res, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", VersionCode: 142, Type: "main"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit1/apks/142/expansionFiles/main",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit1",
	}
	if got := apiCalls(r); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", got, want)
	}
	if n := r.TokenExchanges(); n != 1 {
		t.Errorf("token exchanges = %d, want 1", n)
	}
	var tbl bytes.Buffer
	if err := res.Renderers().Table(&tbl); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(tbl.String(), "referencesVersion: 140") {
		t.Errorf("table %q should show referencesVersion 140", tbl.String())
	}
	if strings.Contains(tbl.String(), "fileSize") {
		t.Errorf("table should not show fileSize when only referencesVersion is set: %q", tbl.String())
	}
	var js bytes.Buffer
	if err := res.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(js.String(), `"referencesVersion":140`) {
		t.Errorf("json %q should be verbatim", js.String())
	}
}
