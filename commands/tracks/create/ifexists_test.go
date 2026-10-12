package create_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/tracks/create"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/config/configtest"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/ifexists"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// existingTrack is a tracks.get body for a track that already exists, with a
// field gplay does not model, so a verbatim pass-through is observable.
const existingTrack = `{"track":"qa-team","releases":[{"name":"7","status":"completed","versionCodes":["7"]}],"unmodeled":true}`

// ifExistsAPI serves the --if-exists flow: the Edit lifecycle, tracks.get
// (getStatus/getResp, 404 by default: the track is absent) and tracks.create.
type ifExistsAPI struct {
	editID    string
	getStatus int
	getResp   string
	fake      *testkit.Fake
}

func (a *ifExistsAPI) serve(t *testing.T) *testkit.Fake {
	t.Helper()
	a.fake = testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q}`, a.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/tracks/"):
			if a.getStatus == 0 {
				return 404, `{"error":{"code":404,"message":"Track not found.","status":"NOT_FOUND"}}`, true
			}
			return a.getStatus, a.getResp, true
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/tracks"):
			return 200, `{"track":"qa-team","releases":[]}`, true
		case strings.HasSuffix(c.Path, ":commit"):
			return 200, fmt.Sprintf(`{"id":%q}`, a.editID), true
		}
		return 0, "", false
	}, testkit.Refuse(t, ""))
	return a.fake
}

func (a *ifExistsAPI) calls() []string {
	var out []string
	for _, c := range a.fake.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

const editsBase = "/androidpublisher/v3/applications/com.example.app/edits"

func assertCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// Absent track + skip: one read in the Edit (404), then the create and the
// commit exactly as without the flag.
func TestRun_ifExistsSkip_absentTrack_readsThenCreates(t *testing.T) {
	api := &ifExistsAPI{editID: "e1"}
	rc, _ := newRC(t, api.serve(t))
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", IfExists: ifexists.Skip})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertCalls(t, api.calls(),
		"POST "+editsBase,
		"GET "+editsBase+"/e1/tracks/qa-team",
		"POST "+editsBase+"/e1/tracks",
		"POST "+editsBase+"/e1:commit",
	)
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if got := js.String(); got != `{"track":"qa-team","releases":[]}` {
		t.Errorf("JSON = %s, want the tracks.create body verbatim", got)
	}
	if !strings.HasPrefix(stderr.String(), "✓ ") {
		t.Errorf("stderr = %q, want the usual ✓ created line", stderr.String())
	}
}

// Existing track + skip, implicit Edit: no create, the Edit is discarded
// rather than committed, stdout is the tracks.get body verbatim (ADR-0003),
// stderr says it was skipped and carries no ✓ (nothing was committed).
func TestRun_ifExistsSkip_existingTrack_noCreateEditDiscarded(t *testing.T) {
	api := &ifExistsAPI{editID: "e1", getStatus: 200, getResp: existingTrack}
	rc, _ := newRC(t, api.serve(t))
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", IfExists: ifexists.Skip})
	if err != nil {
		t.Fatalf("Run: %v (exit %d)", err, exit.For(err))
	}
	assertCalls(t, api.calls(),
		"POST "+editsBase,
		"GET "+editsBase+"/e1/tracks/qa-team",
		"DELETE "+editsBase+"/e1",
	)
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if js.String() != existingTrack {
		t.Errorf("JSON = %s\nwant the tracks.get body verbatim = %s", js.String(), existingTrack)
	}
	got := stderr.String()
	if !strings.Contains(got, `track "qa-team" already exists, skipped`) || strings.Contains(got, "✓") {
		t.Errorf("stderr = %q, want one skipped line and no ✓", got)
	}
	var table bytes.Buffer
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(table.String(), "SKIPPED") || !strings.Contains(table.String(), "qa-team") {
		t.Errorf("table = %q, want a SKIPPED view of qa-team", table.String())
	}
}

// Existing track + skip inside an explicit Edit (`gplay edits begin`): the
// read runs in the pinned Edit and nothing is added to it, committed or
// discarded.
func TestRun_ifExistsSkip_explicitEdit_readsInPinUntouched(t *testing.T) {
	api := &ifExistsAPI{getStatus: 200, getResp: existingTrack}
	rc, _ := newRC(t, api.serve(t))
	fsys := configtest.NewMemFS("/repo", "/home")
	if err := editpin.Write(fsys, "/repo/.gplay", "com.example.app", "edit-pinned"); err != nil {
		t.Fatalf("editpin.Write: %v", err)
	}
	rc.FS = fsys
	rc.Resolved = &config.Resolved{ProjectSharedPath: "/repo/.gplay/config.json"}

	if _, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", IfExists: ifexists.Skip}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertCalls(t, api.calls(), "GET "+editsBase+"/edit-pinned/tracks/qa-team")
}

// A read that fails for another reason than 404 is surfaced and no create is
// sent: existence is never guessed.
func TestRun_ifExistsSkip_readFails_surfacedNoCreate(t *testing.T) {
	api := &ifExistsAPI{editID: "e1", getStatus: 500, getResp: `{"error":{"code":500,"message":"backend"}}`}
	rc, _ := newRC(t, api.serve(t))

	_, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", IfExists: ifexists.Skip})
	if err == nil {
		t.Fatal("Run returned nil, want the tracks.get failure")
	}
	for _, c := range api.calls() {
		if strings.HasPrefix(c, "POST ") && strings.HasSuffix(c, "/tracks") {
			t.Errorf("create sent after a failed read: %v", api.calls())
		}
	}
}

// Default and explicit fail: no read is added, the create runs and its
// "already exists" error is today's (exit 30).
func TestRun_ifExistsFail_noReadTodaysError(t *testing.T) {
	for _, p := range []ifexists.Policy{"", ifexists.Fail} {
		api := &createAPI{
			editID:            "e1",
			trackCreateStatus: 400,
			trackCreateResp:   `{"error":{"code":400,"message":"Track already exists."}}`,
		}
		rc, _ := newRC(t, api.serve(t))

		_, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", IfExists: p})
		if got := exit.For(err); got != 30 {
			t.Errorf("policy %q: exit %d, want 30 (err=%v)", p, got, err)
		}
		for _, c := range api.calls() {
			if strings.HasPrefix(c, "GET ") {
				t.Errorf("policy %q added a read: %v", p, api.calls())
			}
		}
	}
}

// update is not a tracks create policy, and an unknown value is misuse: both
// exit 2 before any HTTP, on the live and the dry-run path.
func TestRun_ifExistsUnoffered_exit2NoHTTP(t *testing.T) {
	for _, p := range []ifexists.Policy{ifexists.Update, "bogus"} {
		for _, dryRun := range []bool{false, true} {
			api := &ifExistsAPI{editID: "e1"}
			rc, _ := newRC(t, api.serve(t))

			_, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", IfExists: p, DryRun: dryRun})
			if got := exit.For(err); got != 2 {
				t.Errorf("%q dryRun=%v: exit %d, want 2 (err=%v)", p, dryRun, got, err)
			}
			if err != nil && !strings.Contains(err.Error(), "skip or fail") {
				t.Errorf("%q: error %q does not name skip or fail", p, err)
			}
			if calls := api.calls(); len(calls) != 0 {
				t.Errorf("%q dryRun=%v: HTTP calls %v, want none", p, dryRun, calls)
			}
		}
	}
}

// --dry-run stays offline under skip and reports the chosen policy without
// resolving existence.
func TestRun_ifExistsSkip_dryRun_offlineReportsPolicy(t *testing.T) {
	api := &ifExistsAPI{editID: "e1"}
	rc, _ := newRC(t, api.serve(t))

	r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", IfExists: ifexists.Skip, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls := api.calls(); len(calls) != 0 {
		t.Errorf("dry-run made HTTP calls %v", calls)
	}
	var js, table bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(js.String(), `"ifExists": "skip"`) {
		t.Errorf("dry-run JSON = %s, want ifExists skip", js.String())
	}
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.Contains(table.String(), "ifExists:    skip") {
		t.Errorf("dry-run table = %q, want the ifExists line", table.String())
	}
}

// The default dry-run preview is byte-identical to before the flag.
func TestRun_dryRun_defaultPolicy_previewUnchanged(t *testing.T) {
	api := &ifExistsAPI{}
	rc, _ := newRC(t, api.serve(t))

	r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.Contains(js.String(), "ifExists") {
		t.Errorf("default dry-run JSON gained ifExists: %s", js.String())
	}
}

// The cobra leaf offers skip|fail, defaults to fail and classifies the flag
// Experimental (the leaf itself stays frozen Public).
func TestNewCommand_ifExistsFlag(t *testing.T) {
	cmd := create.NewCommand(kernel.Boot{})
	f := cmd.Flags().Lookup(ifexists.Name)
	if f == nil {
		t.Fatal("no --if-exists flag")
	}
	if f.Value.Type() != "skip|fail" || f.DefValue != "fail" {
		t.Errorf("--if-exists type=%q default=%q, want skip|fail / fail", f.Value.Type(), f.DefValue)
	}
	if !kernel.IsExperimentalFlag(cmd, f) {
		t.Error("--if-exists is not Experimental")
	}
	if kernel.IsExperimental(cmd) {
		t.Error("tracks create became Experimental; only the flag should be")
	}
}
