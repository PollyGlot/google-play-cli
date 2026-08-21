// Package trackhint_test exercises the shared track-not-found classifier
// that `releases upload` and `releases promote` use to point an operator at
// `gplay tracks create <name>` when they target a closed track that does not
// exist yet.
package trackhint_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/trackhint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// TestClassify_tracksUpdate404_addsCreateHint asserts a tracks.update 404 (the
// signal that the destination closed track has not been created) is wrapped
// with a `gplay tracks create <name>` hint while the underlying
// *api.Error keeps driving the exit code (404 → 30).
func TestClassify_tracksUpdate404_addsCreateHint(t *testing.T) {
	cause := &api.Error{Operation: "tracks.update", Package: "com.example.app", StatusCode: 404, Message: "Track not found."}

	got := trackhint.Classify("qa-team", cause)
	if got == error(cause) {
		t.Fatal("expected a wrapped error carrying the hint, got the cause verbatim")
	}
	if !strings.Contains(got.Error(), "gplay tracks create qa-team") {
		t.Errorf("error %q is missing the `gplay tracks create qa-team` hint", got.Error())
	}
	if code := exit.For(got); code != 30 {
		t.Errorf("exit.For = %d, want 30 (the wrapped *api.Error must stay authoritative)", code)
	}
}

// TestClassify_otherFailures_passThroughVerbatim asserts the hint is scoped
// to the tracks.update-not-found case: a package miss (edits.insert 404), an
// auth failure (tracks.update 403), a non-api error, and nil all propagate
// untouched so no unrelated failure is mislabeled "create the track".
func TestClassify_otherFailures_passThroughVerbatim(t *testing.T) {
	pkgMiss := &api.Error{Operation: "edits.insert", StatusCode: 404}
	if got := trackhint.Classify("qa-team", pkgMiss); got != error(pkgMiss) {
		t.Errorf("edits.insert 404 (package miss) must pass through verbatim, got %v", got)
	}

	auth := &api.Error{Operation: "tracks.update", StatusCode: 403}
	if got := trackhint.Classify("qa-team", auth); got != error(auth) {
		t.Errorf("tracks.update 403 (auth) must pass through verbatim, got %v", got)
	}

	if got := trackhint.Classify("qa-team", nil); got != nil {
		t.Errorf("nil must pass through, got %v", got)
	}
}
