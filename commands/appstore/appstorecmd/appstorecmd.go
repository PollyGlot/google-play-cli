package appstorecmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const FlagStorePackage = "store-package"

const EnvStorePackage = "GPLAY_APP_STORE_PACKAGE"

func RegisterStorePackageFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, FlagStorePackage, "", "package name of the third-party app store making the call (falls back to $"+EnvStorePackage+")")
}

func ResolveStorePackage(flag string) (string, error) {
	if v := strings.TrimSpace(flag); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv(EnvStorePackage)); v != "" {
		return v, nil
	}
	return "", &exit.UsageError{Msg: "no app store package name: pass --" + FlagStorePackage + " <pkg> or export " + EnvStorePackage +
		" (the package name of the app store on whose behalf the request is made, not the app being read or acted on)"}
}

func RequirePlayPackage(pkg, usage string) (string, error) {
	if pkg = strings.TrimSpace(pkg); pkg == "" {
		return "", &exit.UsageError{Msg: usage}
	}
	return pkg, nil
}

func ParseRFC3339(flag, value string) (time.Time, string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Time{}, "", &exit.UsageError{Msg: "missing --" + flag + ": an RFC 3339 timestamp is required, e.g. --" + flag + " 2026-07-01T00:00:00Z"}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, "", exit.Usagef("invalid --%s %q: expected an RFC 3339 timestamp, e.g. 2026-07-01T00:00:00Z or 2026-07-01T02:00:00+02:00", flag, v)
	}
	return t, v, nil
}

func ValidateTimeRange(startFlag, endFlag string) (string, string, error) {
	start, startStr, err := ParseRFC3339("since", startFlag)
	if err != nil {
		return "", "", err
	}
	end, endStr, err := ParseRFC3339("until", endFlag)
	if err != nil {
		return "", "", err
	}
	if !end.After(start) {
		return "", "", exit.Usagef("invalid time range: --until %q must be after --since %q (the range is [start, end), end exclusive)", endStr, startStr)
	}
	return startStr, endStr, nil
}

type forbiddenError struct {
	storePkg string
	cause    error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account cannot read the Play catalog export on behalf of app store %q: verify --store-package names an app store enrolled for the Catalog Export and that the credential is authorized for it: %v", e.storePkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

type notFoundError struct {
	storePkg string
	playPkg  string
	cause    error
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("no catalog app view for Play app %q (app store %q): the app may not be eligible for catalog inclusion, may have been removed from the Play Store, or the app store package name may be wrong: %v", e.playPkg, e.storePkg, e.cause)
}
func (e *notFoundError) Unwrap() error { return e.cause }

func ClassifyAppView(storePkg, playPkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{storePkg: storePkg, cause: err}
		case http.StatusNotFound:
			return &notFoundError{storePkg: storePkg, playPkg: playPkg, cause: err}
		}
	}
	return err
}

type storeNotFoundError struct {
	storePkg string
	cause    error
}

func (e *storeNotFoundError) Error() string {
	return fmt.Sprintf("no catalog export for app store %q: verify --store-package names an app store enrolled for the Google Play Catalog Export: %v", e.storePkg, e.cause)
}
func (e *storeNotFoundError) Unwrap() error { return e.cause }

func ClassifyStoreRead(storePkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{storePkg: storePkg, cause: err}
		case http.StatusNotFound:
			return &storeNotFoundError{storePkg: storePkg, cause: err}
		}
	}
	return err
}

func ClassifyHostedApp(storePkg, pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return &hostedAppNotFoundError{storePkg: storePkg, pkg: pkg, cause: err}
	}
	return ClassifyReview(storePkg, err)
}

type reviewForbiddenError struct {
	storePkg string
	cause    error
}

func (e *reviewForbiddenError) Error() string {
	return fmt.Sprintf("service account cannot act as app store %q: the app store package name must be one Google has enrolled for alternative distribution, and the service account must be linked to that store's Developer account, then retry: %v", e.storePkg, e.cause)
}
func (e *reviewForbiddenError) Unwrap() error { return e.cause }

type reviewStoreNotFoundError struct {
	storePkg string
	cause    error
}

func (e *reviewStoreNotFoundError) Error() string {
	return fmt.Sprintf("app store %q not found: verify the --%s value names the third-party app store's own package name (not the hosted app's): %v", e.storePkg, FlagStorePackage, e.cause)
}
func (e *reviewStoreNotFoundError) Unwrap() error { return e.cause }

type hostedAppNotFoundError struct {
	storePkg string
	pkg      string
	cause    error
}

func (e *hostedAppNotFoundError) Error() string {
	return fmt.Sprintf("no hosted app %q in app store %q: run `gplay appstore create --%s %s --package %s` first (it is the mandatory precondition for every other call here), or verify the --%s value names the app store's own package name (not the hosted app's): %v",
		e.pkg, e.storePkg, FlagStorePackage, e.storePkg, e.pkg, FlagStorePackage, e.cause)
}
func (e *hostedAppNotFoundError) Unwrap() error { return e.cause }

func ClassifyReview(storePkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &reviewForbiddenError{storePkg: storePkg, cause: err}
		case http.StatusNotFound:
			return &reviewStoreNotFoundError{storePkg: storePkg, cause: err}
		}
	}
	return err
}
