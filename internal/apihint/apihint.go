// Package apihint attaches the canonical operator hints to a Play API 403 or
// 404 on the package axis, and classifies the failures of an Edit-scoped call
// in one place.
//
// The wrappers carry no ExitCode of their own: the wrapped *api.Error stays
// authoritative through the exit.Coder chain (403 → exit 11, 404 → exit 30 per
// docs/DESIGN.md §9), so a hint can never move an exit code. Command groups
// keep only their domain wording (a review id, an order, a track) and fall back
// to these for the generic package cases.
package apihint

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// ForbiddenError wraps a 403 on a package with the API-access grant hint.
type ForbiddenError struct {
	Package string
	Cause   error
}

func (e *ForbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q: in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.Package, e.Cause)
}

func (e *ForbiddenError) Unwrap() error { return e.Cause }

// PackageNotFoundError wraps a 404 on a package with a pointer at the
// registered packages.
type PackageNotFoundError struct {
	Package string
	Cause   error
}

func (e *PackageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found: run `gplay apps list` to see the packages registered with gplay: %v", e.Package, e.Cause)
}

func (e *PackageNotFoundError) Unwrap() error { return e.Cause }

// Status reports the HTTP status of the *api.Error in err's chain, or 0 when
// there is none (a transport failure, a local error, nil).
func Status(err error) int {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// Forbidden wraps err with the canonical 403 hint when it carries a 403, and
// returns it untouched otherwise. For groups whose 404 wording is their own.
func Forbidden(pkg string, err error) error {
	if Status(err) == http.StatusForbidden {
		return &ForbiddenError{Package: pkg, Cause: err}
	}
	return err
}

// ForPackage is the Edit error classifier: a 404 means the package is unknown,
// a 403 means the service account lacks access to it, and every other failure
// (5xx, network, decode, a local error) propagates verbatim. A leaf with a
// more specific reading of a status (a missing track, a missing Listing)
// matches it first and delegates the rest here.
func ForPackage(pkg string, err error) error {
	switch Status(err) {
	case http.StatusNotFound:
		return &PackageNotFoundError{Package: pkg, Cause: err}
	case http.StatusForbidden:
		return &ForbiddenError{Package: pkg, Cause: err}
	}
	return err
}
