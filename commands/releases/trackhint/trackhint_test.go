package trackhint_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/trackhint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

func TestClassify_tracksUpdate404_addsCreateHint(t *testing.T) {
	cause := &api.Error{Operation: "tracks.update", Package: "com.example.app", StatusCode: 404, Message: "Track not found."}

	got := trackhint.Classify("qa-team", cause)
	if got == error(cause) { //nolint:errorlint // identity, not errors.Is: a wrapper would also match Is
		t.Fatal("expected a wrapped error carrying the hint, got the cause verbatim")
	}
	if !strings.Contains(got.Error(), "gplay tracks create qa-team") {
		t.Errorf("error %q is missing the `gplay tracks create qa-team` hint", got.Error())
	}
	if code := exit.For(got); code != 30 {
		t.Errorf("exit.For = %d, want 30 (the wrapped *api.Error must stay authoritative)", code)
	}
}

func TestClassify_otherFailures_passThroughVerbatim(t *testing.T) {
	pkgMiss := &api.Error{Operation: "edits.insert", StatusCode: 404}
	if got := trackhint.Classify("qa-team", pkgMiss); got != error(pkgMiss) { //nolint:errorlint // identity, not errors.Is: the test asserts the error is returned verbatim
		t.Errorf("edits.insert 404 (package miss) must pass through verbatim, got %v", got)
	}

	auth := &api.Error{Operation: "tracks.update", StatusCode: 403}
	if got := trackhint.Classify("qa-team", auth); got != error(auth) { //nolint:errorlint // identity, not errors.Is: the test asserts the error is returned verbatim
		t.Errorf("tracks.update 403 (auth) must pass through verbatim, got %v", got)
	}

	if got := trackhint.Classify("qa-team", nil); got != nil {
		t.Errorf("nil must pass through, got %v", got)
	}
}
