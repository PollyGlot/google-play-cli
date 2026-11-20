package apihint

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

type ForbiddenError struct {
	Package string
	Cause   error
}

func (e *ForbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q: in the Play Console, open Setup → API access and grant this service account permission on the app: %v", e.Package, e.Cause)
}

func (e *ForbiddenError) Unwrap() error { return e.Cause }

type PackageNotFoundError struct {
	Package string
	Cause   error
}

func (e *PackageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found: run `gplay apps list` to see the packages registered with gplay: %v", e.Package, e.Cause)
}

func (e *PackageNotFoundError) Unwrap() error { return e.Cause }

func Status(err error) int {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

func Forbidden(pkg string, err error) error {
	if Status(err) == http.StatusForbidden {
		return &ForbiddenError{Package: pkg, Cause: err}
	}
	return err
}

func ForPackage(pkg string, err error) error {
	switch Status(err) {
	case http.StatusNotFound:
		return &PackageNotFoundError{Package: pkg, Cause: err}
	case http.StatusForbidden:
		return &ForbiddenError{Package: pkg, Cause: err}
	}
	return err
}
