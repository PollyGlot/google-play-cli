package orchestrator_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// TestUpload_explicitEdit_reusesPinNoInsertNoCommit is the #48 reuse contract:
// when Opts.ExplicitEditID is set (a `gplay edits begin` pin), the upload
// mutates that already-open Edit and opens/commits/discards NONE of its own.
// The insert/commit/delete handlers fail the test if reached, and the writes
// must target the pinned Edit ID.
func TestUpload_explicitEdit_reusesPinNoInsertNoCommit(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &playRT{
		t:                  t,
		editID:             "edit-should-not-open",
		versionCode:        7,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"7","status":"completed","versionCodes":["7"],"userFraction":1.0}]}`,
		insertHandler: func(*http.Request) (*http.Response, error) {
			t.Fatalf("edits.insert called in explicit mode — the pinned Edit must be reused")
			return nil, nil
		},
		commitHandler: func(*http.Request) (*http.Response, error) {
			t.Fatalf("edits.commit called in explicit mode — the user owns commit")
			return nil, nil
		},
		deleteHandler: func(*http.Request) (*http.Response, error) {
			t.Fatalf("edits.delete called in explicit mode — no auto-discard")
			return nil, nil
		},
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:        "com.example.app",
		Track:          "internal",
		AABPath:        aab,
		ExplicitEditID: "edit-pinned",
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if result.VersionCode != 7 {
		t.Errorf("versionCode = %d, want 7", result.VersionCode)
	}

	// Zero edits.insert calls (the explicit AC), and the writes target the pin.
	var sawBundle, sawTrack bool
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "POST ") && strings.HasSuffix(c, "/edits") {
			t.Errorf("explicit mode must not open an Edit; saw %q", c)
		}
		if strings.Contains(c, "/edits/edit-pinned/") && strings.Contains(c, "/bundles") {
			sawBundle = true
		}
		if strings.Contains(c, "/edits/edit-pinned/tracks/") {
			sawTrack = true
		}
	}
	if !sawBundle || !sawTrack {
		t.Errorf("upload did not target the pinned Edit; calls = %v", rt.calls)
	}
}
