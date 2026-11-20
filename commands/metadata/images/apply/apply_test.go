package imagesapply_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	imagesapply "github.com/PollyGlot/google-play-cli/commands/metadata/images/apply"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type applyRT struct {
	editID   string
	liveBody string
}

func newFake(t *testing.T, r applyRT) *testkit.Fake {
	t.Helper()
	var uploadSeq atomic.Int64
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q}`, r.editID), true
		case strings.HasSuffix(c.Path, ":commit"):
			return 200, `{}`, true
		case c.Method == http.MethodPost && strings.HasPrefix(c.Path, "/upload/"):
			return 200, fmt.Sprintf(`{"image":{"id":"up%d","url":"u","sha256":"s"}}`, uploadSeq.Add(1)), true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/listings/"):
			body := r.liveBody
			if body == "" {
				body = `{"images":[]}`
			}
			return 200, body, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/listings/"):
			return 200, `{"deleted":[]}`, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		}
		t.Errorf("unexpected request: %s %s", c.Method, c.Path)
		return 0, "", false
	})
}

func calls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func committed(f *testkit.Fake) bool {
	for _, c := range f.Calls() {
		if strings.HasSuffix(c.Path, ":commit") {
			return true
		}
	}
	return false
}

func uploads(f *testkit.Fake) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Method == http.MethodPost && strings.HasPrefix(c.Path, "/upload/") {
			n++
		}
	}
	return n
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

func seedIcon(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := imagetree.Write(dir, imagetree.Tree{"en-US": {images.Icon: {testkit.PNG(512, 512)}}}); err != nil {
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

func TestRun_realApply_withoutConfirm_refusesExit3(t *testing.T) {
	rt := newFake(t, applyRT{editID: "e"})
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t)})
	if got := exitCodeOf(t, err); got != 3 {
		t.Errorf("exit = %d, want 3", got)
	}
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) || safety.Flag != "confirm" {
		t.Errorf("err = %v (%T), want *exit.SafetyFlagError naming \"confirm\"", err, err)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("confirm gate must fire before any network, saw %v", calls(rt))
	}
}

func TestRun_dryRun_onlineDiffSchema(t *testing.T) {
	rt := newFake(t, applyRT{editID: "e"})
	rc := newRC(t, rt)
	r, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), DryRun: true, Types: []string{"icon"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if committed(rt) {
		t.Error("dry-run must not commit")
	}
	if uploads(rt) != 0 {
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

func TestRun_realApply_uploadsAndCommits(t *testing.T) {
	rt := newFake(t, applyRT{editID: "e"})
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), Confirm: true, Types: []string{"icon"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if uploads(rt) != 1 {
		t.Errorf("uploads = %d, want 1", uploads(rt))
	}
	if !committed(rt) {
		t.Error("real apply must commit")
	}
}

func TestRun_typeFilter_isATracer(t *testing.T) {
	rt := newFake(t, applyRT{editID: "e"})
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), DryRun: true, Types: []string{"icon"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	listGETs := 0
	for _, c := range calls(rt) {
		if strings.HasPrefix(c, "GET ") && strings.Contains(c, "/listings/") {
			listGETs++
		}
	}
	if listGETs != 1 {
		t.Errorf("--type icon should read exactly 1 slot, got %d: %v", listGETs, calls(rt))
	}
}

func TestRun_validateFailFast(t *testing.T) {
	dir := t.TempDir()
	if err := imagetree.Write(dir, imagetree.Tree{"en-US": {images.Icon: {testkit.PNG(500, 500)}}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rt := newFake(t, applyRT{editID: "e"})
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: dir, DryRun: true, Types: []string{"icon"}})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20 (validate fail-fast)", got)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("validate must fail before any network, saw %v", calls(rt))
	}

	rt2 := newFake(t, applyRT{editID: "e"})
	rc2 := newRC(t, rt2)
	if _, err := imagesapply.Run(rc2, imagesapply.Input{Package: "com.example.app", Dir: dir, DryRun: true, Types: []string{"icon"}, SkipPreflight: true}); err != nil {
		t.Fatalf("--skip-preflight dry-run should pass the pre-check: %v", err)
	}
	if len(rt2.Calls()) == 0 {
		t.Error("--skip-preflight should let the dry-run reach Play")
	}
}

func TestRun_dryRunPrune_showsDeleteRecords(t *testing.T) {
	dir := t.TempDir()
	shot := testkit.PNG(1080, 1920)
	if err := imagetree.Write(dir, imagetree.Tree{"en-US": {images.PhoneScreenshots: {shot}}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	shotSum := sha256.Sum256(shot)
	live := fmt.Sprintf(`{"images":[{"id":"keep","sha256":%q},{"id":"drop","sha256":%q}]}`,
		hex.EncodeToString(shotSum[:]), "00deadbeef")
	rt := newFake(t, applyRT{editID: "e", liveBody: live})
	rc := newRC(t, rt)

	r, err := imagesapply.Run(rc, imagesapply.Input{
		Package: "com.example.app", Dir: dir, DryRun: true, Prune: true, Types: []string{"phoneScreenshots"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if committed(rt) {
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

func TestRun_unknownLocale_isUsageError(t *testing.T) {
	rt := newFake(t, applyRT{editID: "e"})
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), DryRun: true, Locales: []string{"en_US"}})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2 for unknown --locale", got)
	}
	if !strings.Contains(err.Error(), "en_US") {
		t.Errorf("error should name the bad locale: %v", err)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("unknown --locale must be refused before any network, saw %v", calls(rt))
	}
}

func TestRun_unknownType_isUsageError(t *testing.T) {
	rt := newFake(t, applyRT{editID: "e"})
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), DryRun: true, Types: []string{"chromebookScreenshots"}})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2 for unknown --type", got)
	}
}

func TestRun_realApply_emitsConfirmationOnStderr(t *testing.T) {
	rt := newFake(t, applyRT{editID: "e"})
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

func TestRun_dryRun_noConfirmationOnStderr(t *testing.T) {
	rt := newFake(t, applyRT{editID: "e"})
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

func TestRun_promoGraphicType_isUsageError(t *testing.T) {
	rt := newFake(t, applyRT{editID: "e"})
	rc := newRC(t, rt)
	_, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), DryRun: true, Types: []string{"promoGraphic"}})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2 for --type promoGraphic", got)
	}
	if len(calls(rt)) != 0 {
		t.Errorf("--type promoGraphic must be refused before any network, saw %v", calls(rt))
	}
}

func TestRun_retiredPromoGraphicFile_warnsAndIsNeverSent(t *testing.T) {
	dir := seedIcon(t)
	promo := filepath.Join(dir, "en-US", "images", "promoGraphic.png")
	if err := os.WriteFile(promo, testkit.PNG(180, 120), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := newFake(t, applyRT{editID: "e"})
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := imagesapply.Run(rc, imagesapply.Input{Package: "com.example.app", Dir: dir, DryRun: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stderr.String(); !strings.Contains(got, "warning: "+promo+": not an image type Play accepts") {
		t.Errorf("stderr = %q, want a warning naming %s", got, promo)
	}
	for _, c := range calls(rt) {
		if strings.Contains(c, "/promoGraphic") {
			t.Errorf("apply sent a request on the retired promoGraphic slot: %q", c)
		}
	}
}

func TestRun_localeWithoutListing_hintsMetadataApply(t *testing.T) {
	notFound := `{"error":{"code":404,"message":"Listing not found."}}`
	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, `{"id":"e"}`, true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/listings/en-US/"):
			return 404, notFound, true
		case c.Method == http.MethodDelete && strings.HasSuffix(c.Path, "/edits/e"):
			return 204, "", true
		}
		t.Errorf("unexpected request: %s %s", c.Method, c.Path)
		return 0, "", false
	})
	dir := seedIcon(t)
	_, err := imagesapply.Run(newRC(t, fake), imagesapply.Input{Package: "com.example.app", Dir: dir, DryRun: true})
	if err == nil {
		t.Fatal("want the 404 to surface")
	}
	for _, want := range []string{`"en-US"`, "gplay metadata apply", filepath.Join(dir, "en-US")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "gplay apps list") {
		t.Errorf("error %q blames the package", err)
	}
	if got := exitCodeOf(t, err); got != 30 {
		t.Errorf("exit = %d, want 30 (the 404's)", got)
	}
}

func TestRun_aiGenerated_mapsTheTriStateFlag(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name     string
		declared *bool
		want     string
	}{
		{"unset", nil, ""},
		{"true", &yes, "aiGeneratedStateAiGeneratedDeveloperAttested"},
		{"false", &no, "aiGeneratedStateNotAiGenerated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newFake(t, applyRT{editID: "e"})
			if _, err := imagesapply.Run(newRC(t, rt), imagesapply.Input{Package: "com.example.app", Dir: seedIcon(t), Confirm: true, Types: []string{"icon"}, AIGenerated: tc.declared}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			var upload *testkit.Call
			for _, c := range rt.Calls() {
				if strings.HasPrefix(c.Path, "/upload/") {
					upload = &c
				}
			}
			if upload == nil {
				t.Fatalf("no upload among %v", calls(rt))
			}
			q, err := url.ParseQuery(upload.Query)
			if err != nil {
				t.Fatalf("parse query: %v", err)
			}
			if _, sent := q["aiGeneratedState"]; sent != (tc.want != "") || q.Get("aiGeneratedState") != tc.want {
				t.Errorf("upload query %q, want aiGeneratedState=%q", upload.Query, tc.want)
			}
		})
	}
}
