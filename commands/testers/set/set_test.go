// Package set_test exercises `gplay testers set` at the kernel level: a
// RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// promote / upload write-command harness so a single seam proves the auth +
// Edit lifecycle wiring (open → testers.update → commit) for testers set.
package set_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/testers/set"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// setRT terminates the OAuth2 /token exchange and routes every
// androidpublisher call needed by a testers replacement:
// edits.insert, testers.update (PUT, body captured), edits.commit, and
// edits.delete (the failure/discard path).
type setRT struct {
	t                 *testing.T
	editID            string
	testersUpdateResp string

	mu               sync.Mutex
	calls            []string
	tokenHits        int
	testersUpdateReq []byte
}

func (r *setRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}

	r.calls = append(r.calls, req.Method+" "+req.URL.Path)

	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/testers/"):
		body, _ := io.ReadAll(req.Body)
		r.testersUpdateReq = body
		resp := r.testersUpdateResp
		if resp == "" {
			resp = `{"googleGroups":[]}`
		}
		return jsonResp(200, resp), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, r.editID)), nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// signedSAJSON returns a minimal but well-formed service-account JSON with
// a real RSA key — enough for token.Source to mint a signed JWT.
func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, err := json.Marshal(map[string]any{
		"type":         "service_account",
		"project_id":   "test-proj",
		"private_key":  string(pemBytes),
		"client_email": "playci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, &stdout
}

// TestRun_bareSet_exit2_noHTTP asserts the footgun guard: a bare `set`
// with neither --group nor --clear is misuse — it must short-circuit with
// exit 2 before any HTTP, so a forgotten --group can never silently wipe
// the list.
func TestRun_bareSet_exit2_noHTTP(t *testing.T) {
	rt := &setRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := set.Run(rc, set.Input{Package: "com.example.app", Track: "qa-team"})
	if err == nil {
		t.Fatal("Run returned nil error; want usage error")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_groupAndClear_exit2 asserts --group and --clear together is
// contradictory — exit 2 before any HTTP.
func TestRun_groupAndClear_exit2(t *testing.T) {
	rt := &setRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := set.Run(rc, set.Input{
		Package:   "com.example.app",
		Track:     "qa-team",
		Groups:    []string{"a@googlegroups.com"},
		GroupsSet: true,
		Clear:     true,
	})
	if err == nil {
		t.Fatal("Run returned nil error; want usage error")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_clear_putsEmptyArray asserts --clear writes an empty audience:
// the full open → PUT → commit sequence runs and the captured PUT body
// carries an explicit empty array (NOT null).
func TestRun_clear_putsEmptyArray(t *testing.T) {
	rt := &setRT{
		t:                 t,
		editID:            "edit-clear",
		testersUpdateResp: `{"googleGroups":[]}`,
	}
	rc, _ := newRC(t, rt)

	r, err := set.Run(rc, set.Input{
		Package: "com.example.app",
		Track:   "qa-team",
		Clear:   true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on --clear")
	}

	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-clear/testers/qa-team",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-clear:commit",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}
	body := string(rt.testersUpdateReq)
	if !strings.Contains(body, `"googleGroups":[]`) {
		t.Errorf("testers.update body = %s, want it to contain \"googleGroups\":[]", body)
	}
}

// TestRun_setGroups_putsList asserts `--group a,b` writes [a,b]: the PUT
// body carries both groups and --output json is the raw testers.update
// response (ADR-0003 pass-through).
func TestRun_setGroups_putsList(t *testing.T) {
	rt := &setRT{
		t:                 t,
		editID:            "edit-set",
		testersUpdateResp: `{"googleGroups":["a@googlegroups.com","b@googlegroups.com"]}`,
	}
	rc, _ := newRC(t, rt)

	r, err := set.Run(rc, set.Input{
		Package:   "com.example.app",
		Track:     "qa-team",
		Groups:    []string{"a@googlegroups.com", "b@googlegroups.com"},
		GroupsSet: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on --group set")
	}

	body := string(rt.testersUpdateReq)
	if !strings.Contains(body, "a@googlegroups.com") || !strings.Contains(body, "b@googlegroups.com") {
		t.Errorf("testers.update body = %s, want both groups", body)
	}

	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(rt.testersUpdateResp) {
		t.Errorf("JSON output = %s\nwant raw testers.update payload = %s", got, rt.testersUpdateResp)
	}
}

// TestRun_dryRun_noHTTP asserts --dry-run previews the target audience
// without any HTTP call and still returns a non-nil Renderable whose view
// shows the groups that would be written.
func TestRun_dryRun_noHTTP(t *testing.T) {
	rt := &setRT{t: t}
	rc, _ := newRC(t, rt)

	r, err := set.Run(rc, set.Input{
		Package:   "com.example.app",
		Track:     "qa-team",
		Groups:    []string{"a@googlegroups.com", "b@googlegroups.com"},
		GroupsSet: true,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on --dry-run")
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls on --dry-run, saw: %v", rt.calls)
	}

	var tableOut bytes.Buffer
	if err := r.Renderers().Table(&tableOut); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := tableOut.String()
	if !strings.Contains(out, "a@googlegroups.com") || !strings.Contains(out, "b@googlegroups.com") {
		t.Errorf("dry-run preview = %q, want both target groups", out)
	}
}

// TestNewCommand_registersExpectedFlags is a thin smoke test for the
// cobra wiring: every write flag exists, there is NO --confirm, and the
// command is named "set".
func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := set.NewCommand(kernel.Boot{})
	for _, name := range []string{
		"package",
		"track",
		"group",
		"clear",
		"dry-run",
		"keep-edit-on-failure",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if cmd.Flags().Lookup("confirm") != nil {
		t.Error("cobra command has a --confirm flag; testers set must NOT have one")
	}
	if got := cmd.Use; got != "set" {
		t.Errorf("cmd.Use = %q, want %q", got, "set")
	}
}
