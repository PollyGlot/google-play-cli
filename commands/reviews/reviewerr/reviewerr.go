package reviewerr

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("Reply to reviews permission required for %q: in the Play Console open Users and permissions, edit this service account, and grant the \"Reply to reviews\" app permission: %v", e.pkg, e.cause)
}

func (e *forbiddenError) Unwrap() error { return e.cause }

type reviewNotFoundError struct {
	reviewID string
	cause    error
}

func (e *reviewNotFoundError) Error() string {
	return fmt.Sprintf("review %q not found: check the id, or note the reviews API only exposes the last 7 days so an older review can no longer be replied to: %v", e.reviewID, e.cause)
}

func (e *reviewNotFoundError) Unwrap() error { return e.cause }

type reviewLookupNotFoundError struct {
	reviewID string
	cause    error
}

func (e *reviewLookupNotFoundError) Error() string {
	return fmt.Sprintf("review %q not found: check the id, or note the reviews API only exposes the last 7 days, so a valid but older review can no longer be fetched: %v", e.reviewID, e.cause)
}

func (e *reviewLookupNotFoundError) Unwrap() error { return e.cause }

func Classify(pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		case http.StatusNotFound:
			return &apihint.PackageNotFoundError{Package: pkg, Cause: err}
		}
	}
	return err
}

func ClassifyReply(pkg, reviewID string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		case http.StatusNotFound:
			return &reviewNotFoundError{reviewID: reviewID, cause: err}
		}
	}
	return err
}

func ClassifyView(pkg, reviewID string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		case http.StatusNotFound:
			return &reviewLookupNotFoundError{reviewID: reviewID, cause: err}
		}
	}
	return err
}
