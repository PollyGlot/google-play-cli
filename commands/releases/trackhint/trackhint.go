// Package trackhint maps the one operator-facing failure that `releases
// upload` and `releases promote` share when they target a custom closed
// track that has not been created yet — a tracks.update 404 — to an
// actionable error pointing at `gplay tracks create <name>`. It is shared by
// both verbs so an upload and a promote surface the same guidance for the
// same upstream miss. Like the tracks/releases classifiers, the hint wrapper
// carries no ExitCode of its own: the wrapped *api.Error stays authoritative
// through the Coder chain (404 → exit 30 per docs/DESIGN.md §9).
//
// gplay never auto-creates a track as a side effect of an upload/promote: a
// typo'd --track must fail loudly (this hint), not silently spawn a phantom
// track. See docs/DESIGN.md §10 and PRD #117.
package trackhint

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// opTracksUpdate is the play-layer operation name internal/play/tracks
// stamps on the *api.Error from edits.tracks.update. Matching on it (rather
// than on the bare 404) keeps the create hint scoped to the destination
// closed-track miss and OFF the look-alike 404s on the same flow: a package
// miss at edits.insert and a malformed-AAB miss at bundles.upload would
// otherwise be mislabeled "create the track".
const opTracksUpdate = "tracks.update"

// trackNotFoundError wraps a tracks.update 404 with the `gplay tracks create`
// hint. It carries no ExitCode of its own so the wrapped *api.Error (404 →
// exit 30) stays authoritative through the Coder chain.
type trackNotFoundError struct {
	track string
	cause error
}

// Error renders the not-found message plus the create hint, naming the track
// twice so the operator can copy the exact `gplay tracks create <name>` line.
func (e *trackNotFoundError) Error() string {
	return fmt.Sprintf("track %q does not exist — create it first with `gplay tracks create %s`, then re-run (gplay never auto-creates a track as a side effect of an upload/promote): %v", e.track, e.track, e.cause)
}

// Unwrap exposes the underlying *api.Error so the Coder chain keeps mapping
// the 404 to exit 30.
func (e *trackNotFoundError) Unwrap() error { return e.cause }

// Classify attaches a `gplay tracks create <track>` hint to a tracks.update
// 404 — the signal that an upload/promote targeted a closed track that has
// not been created yet — while leaving the wrapped *api.Error to drive the
// exit code. Every other failure (a package miss at edits.insert, a
// malformed AAB at bundles.upload, an auth 403, a 5xx, an ambiguous-release
// state conflict, a non-api error) propagates verbatim. nil stays nil.
func Classify(track string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr.Operation == opTracksUpdate && apiErr.StatusCode == http.StatusNotFound {
		return &trackNotFoundError{track: track, cause: err}
	}
	return err
}
