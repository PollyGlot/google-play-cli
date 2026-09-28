package begin_test

import (
	"net/http"
	"strings"
	"testing"

	begincmd "github.com/PollyGlot/google-play-cli/commands/edits/begin"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRun_pinWriteAndDiscardBothFail_reportsTheOrphanedEdit drives the worst
// case of begin: the Edit is open on Play, the pin could not be written, and
// the rollback discard failed too. Nothing local can recover that Edit, so
// the error must carry both causes and the Edit ID with the way out.
func TestRun_pinWriteAndDiscardBothFail_reportsTheOrphanedEdit(t *testing.T) {
	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, `{"id":"edit-orphan","expiryTimeSeconds":"1700000000"}`, true
		case c.Method == http.MethodDelete && strings.HasSuffix(c.Path, "/edits/edit-orphan"):
			return 500, `{"error":{"code":500,"message":"backend error"}}`, true
		}
		return 0, "", false
	})
	rc, gplayDir := newRC(t, fake)
	rc.FS = failWriteFS{config.OSFS{}}

	_, err := begincmd.Run(rc, begincmd.Input{Package: pkg})
	if err == nil {
		t.Fatal("expected the pin-write failure to surface")
	}
	msg := err.Error()
	for _, want := range []string{"disk full", "cleanup also failed", "edit-orphan is still open", "backend error"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
	var deletes int
	for _, c := range fake.Calls() {
		if c.Method == http.MethodDelete {
			deletes++
		}
	}
	if deletes != 1 {
		t.Errorf("DELETE calls = %d, want 1 rollback attempt", deletes)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("a failed begin must leave no pin behind")
	}
}
