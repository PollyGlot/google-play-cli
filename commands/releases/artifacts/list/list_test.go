package list_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	listcmd "github.com/PollyGlot/google-play-cli/commands/releases/artifacts/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/config/configtest"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const (
	apksBody    = `{"kind":"androidpublisher#apksListResponse","apks":[{"versionCode":7,"binary":{"sha1":"a1","sha256":"apk-7"}}]}`
	bundlesBody = `{"kind":"androidpublisher#bundlesListResponse","bundles":[{"versionCode":9,"sha256":"aab-9"},{"versionCode":5,"sha256":"aab-5"}]}`
)

// newArtFake fakes the Edit lifecycle plus both list endpoints; the Fake
// records the sequence so tests can assert insert → lists → delete.
func newArtFake() *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, `{"id":"edit-ro","expiryTimeSeconds":"1700000000"}`, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return http.StatusNoContent, "", true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/apks"):
			return http.StatusOK, apksBody, true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/bundles"):
			return http.StatusOK, bundlesBody, true
		}
		return 0, "", false
	})
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// apiCalls lists the recorded API calls (token exchanges excluded) as
// "METHOD path".
func apiCalls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

// TestRun_bothKinds_readOnlyEdit_rowsByVersionCode asserts the lifecycle
// (insert → apks → bundles → delete, never commit), the merged table ordered
// by versionCode, and the {"apks","bundles"} JSON envelope of verbatim bodies.
func TestRun_bothKinds_readOnlyEdit_rowsByVersionCode(t *testing.T) {
	rt := newArtFake()
	rc := newRC(t, rt)
	r, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-ro/apks",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-ro/bundles",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-ro",
	}
	if got := apiCalls(rt); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v", got, want)
	}

	var table bytes.Buffer
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("Table: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(table.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("table = %q, want header + 3 rows", table.String())
	}
	for i, prefix := range []string{"bundle", "apk", "bundle"} {
		if !strings.HasPrefix(lines[i+1], prefix) {
			t.Errorf("row %d = %q, want kind %s", i, lines[i+1], prefix)
		}
	}
	if !strings.Contains(lines[1], "5") || !strings.Contains(lines[2], "7") || !strings.Contains(lines[3], "9") {
		t.Errorf("rows not in versionCode order: %q", lines[1:])
	}

	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var env struct {
		Apks    json.RawMessage `json:"apks"`
		Bundles json.RawMessage `json:"bundles"`
	}
	if err := json.Unmarshal(js.Bytes(), &env); err != nil {
		t.Fatalf("json envelope: %v: %s", err, js.String())
	}
	if string(env.Apks) != apksBody || string(env.Bundles) != bundlesBody {
		t.Errorf("json values must be the verbatim API bodies: %s", js.String())
	}
}

// TestRun_kindApk_sendsOnlyApksRequest asserts --kind apk skips bundles.list
// and passes the apks response through verbatim.
func TestRun_kindApk_sendsOnlyApksRequest(t *testing.T) {
	rt := newArtFake()
	rc := newRC(t, rt)
	r, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", Kind: "apk"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := apiCalls(rt)
	for _, c := range calls {
		if strings.HasSuffix(c, "/bundles") {
			t.Errorf("--kind apk must not call bundles.list; calls=%v", calls)
		}
	}
	sawApks := false
	for _, c := range calls {
		if strings.HasSuffix(c, "/apks") {
			sawApks = true
		}
	}
	if !sawApks {
		t.Errorf("apks.list not called; calls=%v", calls)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if js.String() != apksBody {
		t.Errorf("json = %s, want the verbatim apks.list body", js.String())
	}
}

// TestRun_explicitPin_readsInsidePinnedEdit_noInsertNoDelete asserts a
// `gplay edits begin` pin is reused untouched: no insert, no delete.
func TestRun_explicitPin_readsInsidePinnedEdit_noInsertNoDelete(t *testing.T) {
	rt := newArtFake()
	rc := newRC(t, rt)
	fsys := configtest.NewMemFS("/repo", "/home")
	if err := editpin.Write(fsys, "/repo/.gplay", "com.example.app", "edit-pinned"); err != nil {
		t.Fatalf("editpin.Write: %v", err)
	}
	rc.FS = fsys
	rc.Resolved = &config.Resolved{ProjectSharedPath: "/repo/.gplay/config.json"}
	if _, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", Kind: "bundle"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"GET /androidpublisher/v3/applications/com.example.app/edits/edit-pinned/bundles"}
	if got := apiCalls(rt); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want %v (pinned Edit reused, never inserted or discarded)", got, want)
	}
}

// TestRun_badKind_exit2_noNetwork asserts --kind is validated before any HTTP.
func TestRun_badKind_exit2_noNetwork(t *testing.T) {
	rt := newArtFake()
	rc := newRC(t, rt)
	_, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", Kind: "aab"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
	if len(rt.Calls()) != 0 || rt.TokenExchanges() != 0 {
		t.Errorf("must not reach the network; calls=%v, token exchanges=%d", apiCalls(rt), rt.TokenExchanges())
	}
}

// TestRun_noPackage_exit2 asserts the package resolution guard.
func TestRun_noPackage_exit2(t *testing.T) {
	rt := newArtFake()
	rc := newRC(t, rt)
	_, err := listcmd.Run(rc, listcmd.Input{})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
}
