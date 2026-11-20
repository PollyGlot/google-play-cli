package bucket

import (
	"errors"
	"net/http"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/team/addressing"
)

type Permission string

const (
	ViewAppInformation Permission = "View app information"
	ViewFinancialData  Permission = "View financial data"
)

func Name(developerID string) string { return "pubsite_prod_rev_" + developerID }

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

type AccessError struct {
	Bucket     string
	Permission Permission
	Page       string
	Cause      error
}

func (e *AccessError) Error() string {
	return "cannot read the reporting bucket " + e.Bucket +
		": grant the service account the \"" + string(e.Permission) + "\" (global) permission in the Play Console, " +
		"and if the bucket name differs, copy the authoritative gs:// URI from the Console's Download reports > " + e.Page + " page " +
		"(\"Copy Cloud Storage URI\" button) and pass the bucket name (the URI without gs://) via --bucket: " + e.Cause.Error()
}

func (e *AccessError) Unwrap() error { return e.Cause }

func Classify(bucket string, perm Permission, page string, err error) error {
	if IsForbidden(err) || IsNotFound(err) {
		return &AccessError{Bucket: bucket, Permission: perm, Page: page, Cause: err}
	}
	return err
}

func IsNotFound(err error) bool { return statusOf(err) == http.StatusNotFound }

func IsForbidden(err error) bool { return statusOf(err) == http.StatusForbidden }

func statusOf(err error) int {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}
