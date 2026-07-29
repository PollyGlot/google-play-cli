// Package imagesapply_test exercises `gplay metadata images apply` end to end
// at the kernel level: the --confirm gate, the online --dry-run diff schema,
// the offline validate pre-check, the --type tracer, and a real apply that
// uploads a slot inside one committed Edit.
package imagesapply_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	imagesapply "github.com/PollyGlot/google-play-cli/commands/metadata/images/apply"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

func pngOf(w, h int) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

// applyRT routes the apply sequence. images.list returns empty for every slot
// (so a local image is a fresh upload). It records uploads, deleteall calls,
// and whether a commit happened, and fails on nothing it does not recognize.
type applyRT struct {
	t        *testing.T
	editID   string
	liveBody string // images.list body for every slot (default: empty)

	mu        sync.Mutex
	uploads   int
	deleteAll int
	committed bool
	calls     []string
}

func (r *applyRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"abc","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q}`, r.editID)), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		r.committed = true
		return jsonResp(200, `{}`), nil
	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/upload/"):
		r.uploads++
		_, _ = io.Copy(io.Discard, req.Body)
		return jsonResp(200, fmt.Sprintf(`{"image":{"id":"up%d","url":"u","sha256":"s"}}`, r.uploads)), nil
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/listings/"):
		body := r.liveBody
		if body == "" {
			body = `{"images":[]}` // every slot empty live by default
		}
		return jsonResp(200, body), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/listings/"):
		r.deleteAll++
		return jsonResp(200, `{"deleted":[]}`), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{
		"type": "service_account", "project_id": "p",
		"private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com",
		"token_uri": "https://oauth2.googleapis.com/token",
	})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// seedIcon writes a valid 512×512 icon for en-US and returns the dir.
func seedIcon(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := imagetree.Write(dir, imagetree.Tree{"en-US": {images.Icon: {pngOf(512, 512)}}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return dir
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var coder interface{ ExitCode() int }
	for e := err; e != nil; {
		if c, ok := e.(interface{ ExitCode() int }); ok {
			coder = c
			break
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	if coder == nil {
		t.Fatalf("err %v (%T) has no ExitCode()", err, err)
	}
	return coder.ExitCode()
}

// TestRun_realApply_withoutConfirm_refusesExit3 asserts the confirm gate fires
// before any network, with exit 3 (safety flag required, docs/DESIGN.md §9 —
// NOT the generic usage exit 2, #408).
func TestRun_realApply_withoutConfirm_refusesExit3(t *testing.T) {
	rt := &applyRT{t: t, editID: "e"}
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t)})
	if got := exitCodeOf(t, err); got != 3 {
		t.Errorf("exit = %d, want 3", got)
	}
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) || safety.Flag != "confirm" {
		t.Errorf("err = %v (%T), want *exit.SafetyFlagError naming \"confirm\"", err, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("confirm gate must fire before any network, saw %v", rt.calls)
	}
}

// TestRun_dryRun_onlineDiffSchema asserts --dry-run reads live and emits the
// ADR-0013 diff schema (a fresh icon → summary.upload ≥ 1, the jq gate true)
// without committing.
func TestRun_dryRun_onlineDiffSchema(t *testing.T) {
	rt := &applyRT{t: t, editID: "e"}
	rc := newRC(t, rt)
	r, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), DryRun: true, Types: []string{"icon"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.committed {
		t.Error("dry-run must not commit")
	}
	if rt.uploads != 0 {
		t.Error("dry-run must not upload")
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var doc struct {
		Package string `json:"package"`
		Summary struct {
			Upload, Delete, Reorder int
		} `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("not the diff schema: %v\n%s", err, buf.String())
	}
	if doc.Summary.Upload+doc.Summary.Delete+doc.Summary.Reorder <= 0 {
		t.Errorf("CI gate should be > 0 for a fresh icon: %+v", doc.Summary)
	}
}

// TestRun_realApply_uploadsAndCommits asserts a confirmed apply uploads the
// local icon and commits once inside one Edit.
func TestRun_realApply_uploadsAndCommits(t *testing.T) {
	rt := &applyRT{t: t, editID: "e"}
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), Confirm: true, Types: []string{"icon"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.uploads != 1 {
		t.Errorf("uploads = %d, want 1", rt.uploads)
	}
	if !rt.committed {
		t.Error("real apply must commit")
	}
}

// TestRun_typeFilter_isATracer asserts --type icon considers only the icon
// slot (one images.list GET), the single-slot tracer.
func TestRun_typeFilter_isATracer(t *testing.T) {
	rt := &applyRT{t: t, editID: "e"}
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), DryRun: true, Types: []string{"icon"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	listGETs := 0
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "GET ") && strings.Contains(c, "/listings/") {
			listGETs++
		}
	}
	if listGETs != 1 {
		t.Errorf("--type icon should read exactly 1 slot, got %d: %v", listGETs, rt.calls)
	}
}

// TestRun_validateFailFast asserts a bad image (500×500 icon) is rejected by
// the offline pre-check (exit 20) before any network, and that --no-validate
// bypasses it.
func TestRun_validateFailFast(t *testing.T) {
	dir := t.TempDir()
	if err := imagetree.Write(dir, imagetree.Tree{"en-US": {images.Icon: {pngOf(500, 500)}}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rt := &applyRT{t: t, editID: "e"}
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: dir, DryRun: true, Types: []string{"icon"}})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20 (validate fail-fast)", got)
	}
	if len(rt.calls) != 0 {
		t.Errorf("validate must fail before any network, saw %v", rt.calls)
	}

	// --no-validate bypasses the pre-check: the dry-run now reaches Play.
	rt2 := &applyRT{t: t, editID: "e"}
	rc2 := newRC(t, rt2)
	if _, err := imagesapply.Run(rc2, imagesapply.Input{Package: "com.example.app", Dir: dir, DryRun: true, Types: []string{"icon"}, NoValidate: true}); err != nil {
		t.Fatalf("--no-validate dry-run should pass the pre-check: %v", err)
	}
	if len(rt2.calls) == 0 {
		t.Error("--no-validate should let the dry-run reach Play")
	}
}

// TestRun_dryRunPrune_showsDeleteRecords asserts `--dry-run --prune --output
// json` surfaces an online-only image as a delete record in the ADR-0013
// schema (the #135 jq gate), without committing.
func TestRun_dryRunPrune_showsDeleteRecords(t *testing.T) {
	dir := t.TempDir()
	shot := pngOf(1080, 1920)
	if err := imagetree.Write(dir, imagetree.Tree{"en-US": {images.PhoneScreenshots: {shot}}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	shotSum := sha256.Sum256(shot)
	// Live has the local shot plus an online-only one; --prune should delete it.
	live := fmt.Sprintf(`{"images":[{"id":"keep","sha256":%q},{"id":"drop","sha256":%q}]}`,
		hex.EncodeToString(shotSum[:]), "00deadbeef")
	rt := &applyRT{t: t, editID: "e", liveBody: live}
	rc := newRC(t, rt)

	r, err := imagesapply.Run(rc, imagesapply.Input{
		Package: "com.example.app", Dir: dir, DryRun: true, Prune: true, Types: []string{"phoneScreenshots"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.committed {
		t.Error("dry-run must not commit")
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var doc struct {
		Slots []struct {
			Op string `json:"op"`
		} `json:"slots"`
		Summary struct {
			Delete int `json:"delete"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("not the diff schema: %v\n%s", err, buf.String())
	}
	if doc.Summary.Delete < 1 {
		t.Errorf("prune dry-run should report a delete: %s", buf.String())
	}
	hasDelete := false
	for _, s := range doc.Slots {
		if s.Op == "delete" {
			hasDelete = true
		}
	}
	if !hasDelete {
		t.Errorf("prune dry-run should carry a delete record: %s", buf.String())
	}
}

// TestRun_unknownLocale_isUsageError asserts a typo'd --locale is refused
// upfront (exit 2) instead of silently reconciling nothing — the footgun the
// --type guard already prevents.
func TestRun_unknownLocale_isUsageError(t *testing.T) {
	rt := &applyRT{t: t, editID: "e"}
	rc := newRC(t, rt)
	// seedIcon writes en-US; ask for en_US (underscore typo).
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), DryRun: true, Locales: []string{"en_US"}})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2 for unknown --locale", got)
	}
	if !strings.Contains(err.Error(), "en_US") {
		t.Errorf("error should name the bad locale: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("unknown --locale must be refused before any network, saw %v", rt.calls)
	}
}

// TestRun_unknownType_isUsageError asserts a typo'd --type is refused upfront.
func TestRun_unknownType_isUsageError(t *testing.T) {
	rt := &applyRT{t: t, editID: "e"}
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), DryRun: true, Types: []string{"chromebookScreenshots"}})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2 for unknown --type", got)
	}
}

// TestRun_realApply_emitsConfirmationOnStderr asserts a committed image apply
// prints a single ✓ line on stderr (DESIGN §8) naming the package and the
// upload/delete tally, alongside the stdout payload.
func TestRun_realApply_emitsConfirmationOnStderr(t *testing.T) {
	rt := &applyRT{t: t, editID: "e"}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), Confirm: true, Types: []string{"icon"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "com.example.app") || !strings.Contains(got, "uploaded") {
		t.Errorf("images apply ✓ line wrong:\n%s", got)
	}
}

// TestRun_dryRun_noConfirmationOnStderr asserts --dry-run never emits a ✓.
func TestRun_dryRun_noConfirmationOnStderr(t *testing.T) {
	rt := &applyRT{t: t, editID: "e"}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), DryRun: true, Types: []string{"icon"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}
