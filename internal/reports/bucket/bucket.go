// Package bucket is the plumbing every reader of the Reporting bucket shares:
// `reviews history` (ADR-0037) and the `reports` families (ADR-0050). It
// derives the bucket name from the developer-account axis, and turns a 403/404
// from the bucket into the one actionable message: which Play Console
// permission the service account lacks, and where the authoritative bucket
// URI is copied from.
package bucket

import (
	"errors"
	"net/http"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/team/addressing"
)

// Permission is the global Play Console permission a family of exports
// requires, worded as the Console's Users and permissions page shows it.
type Permission string

const (
	// ViewAppInformation reads reviews and the statistics exports.
	ViewAppInformation Permission = "View app information"
	// ViewFinancialData reads the earnings, sales and subscription exports.
	ViewFinancialData Permission = "View financial data"
)

// Name is the default Reporting bucket of a developer account. The Console's
// "Copy Cloud Storage URI" button is the authority; in practice its suffix is
// the numeric developer-id (ADR-0015), which --bucket overrides when not.
func Name(developerID string) string { return "pubsite_prod_rev_" + developerID }

// Resolve returns the bucket a command reads: the --bucket flag when given,
// else the name derived from the developer-id cascade (--developer-id, env,
// Account, project-local). --bucket alone skips that cascade entirely.
func Resolve(rc *kernel.RunContext, bucketFlag, developerIDFlag string) (string, error) {
	if b := strings.TrimSpace(bucketFlag); b != "" {
		return b, nil
	}
	devID, err := addressing.ForRun(rc, developerIDFlag)
	if err != nil {
		return "", err
	}
	return Name(devID), nil
}

// AccessError wraps a 403/404 from the Reporting bucket with what an operator
// needs to fix it: the permission to grant and the Console page whose "Copy
// Cloud Storage URI" button gives the real bucket name. It has no ExitCode of
// its own, so the wrapped *api.Error stays authoritative (403 is exit 11, 404
// exit 30).
type AccessError struct {
	Bucket     string
	Permission Permission
	// Page is the Console's Download reports page that lists the exports:
	// "Reviews", "Statistics" or "Financial".
	Page  string
	Cause error
}

func (e *AccessError) Error() string {
	return "cannot read the reporting bucket " + e.Bucket +
		": grant the service account the \"" + string(e.Permission) + "\" (global) permission in the Play Console, " +
		"and if the bucket name differs, copy the authoritative gs:// URI from the Console's Download reports > " + e.Page + " page " +
		"(\"Copy Cloud Storage URI\" button) and pass the bucket name (the URI without gs://) via --bucket: " + e.Cause.Error()
}

func (e *AccessError) Unwrap() error { return e.Cause }

// Classify turns a 403/404 from the bucket into an *AccessError and lets every
// other failure through verbatim.
func Classify(bucket string, perm Permission, page string, err error) error {
	if IsForbidden(err) || IsNotFound(err) {
		return &AccessError{Bucket: bucket, Permission: perm, Page: page, Cause: err}
	}
	return err
}

// IsNotFound reports a 404 from the bucket: a missing object, or a bucket that
// does not exist.
func IsNotFound(err error) bool { return statusOf(err) == http.StatusNotFound }

// IsForbidden reports a 403 from the bucket: the service account lacks the
// permission the export requires.
func IsForbidden(err error) bool { return statusOf(err) == http.StatusForbidden }

func statusOf(err error) int {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}
