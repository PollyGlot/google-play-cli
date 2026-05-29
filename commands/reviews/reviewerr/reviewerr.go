// Package reviewerr maps the two operator-facing failures of the Reviews
// API — a service account lacking the "Reply to reviews" permission (403)
// and an unknown package (404) — to actionable, hinted errors. It is shared
// by `reviews list` and `reviews reply` so both verbs surface the same
// guidance for the same upstream status. Like the tracks/releases
// classifiers, the hint wrappers carry no ExitCode of their own: the wrapped
// *api.Error stays authoritative through the Coder chain (403 → exit 11,
// 404 → exit 30 per docs/DESIGN.md §9).
package reviewerr

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// forbiddenError wraps a 403 with the "Reply to reviews" grant hint. The
// Reviews API gates both reading and replying behind the single Play Console
// "Reply to reviews" permission, so a 403 on reviews.list means the same
// missing grant a reply would hit.
type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("Reply to reviews permission required for %q — in the Play Console open Users and permissions, edit this service account, and grant the \"Reply to reviews\" app permission: %v", e.pkg, e.cause)
}

func (e *forbiddenError) Unwrap() error { return e.cause }

// packageNotFoundError wraps a 404 with a hint pointing at `gplay apps list`.
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found — run `gplay apps list` to see the packages registered with gplay: %v", e.pkg, e.cause)
}

func (e *packageNotFoundError) Unwrap() error { return e.cause }

// Classify adds an actionable hint to a 403 or 404 from a reviews call while
// leaving the wrapped *api.Error to drive the exit code. Every other failure
// (5xx, network, decode) propagates verbatim.
func Classify(pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		case http.StatusNotFound:
			return &packageNotFoundError{pkg: pkg, cause: err}
		}
	}
	return err
}
