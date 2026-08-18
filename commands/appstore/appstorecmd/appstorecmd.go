// Package appstorecmd holds the wiring shared by every `gplay appstore` leaf:
// app store package name resolution, the exit-2 usage error, RFC 3339 time
// validation, and the 403/404 hint classifications that turn a bare API
// rejection into an agent-resolvable refusal. Mirrors commands/orders/orderscmd
// and commands/games/gamescmd, and keeps the leaves thin.
//
// The namespace wraps two sibling surfaces sharing one addressing axis:
// `appstorecatalog` (read-only catalog export) and `appstoreappsreview` (the
// hosted app review path — see CONTEXT.md "Hosted app" and PRD #377).
//
// Addressing rides the APP STORE PACKAGE NAME — the package of the alternative
// app store on whose behalf the request is made — which is a different axis
// from the Android package the rest of gplay targets: it names the *caller's
// store*, not the app being read or acted on. There is therefore no
// `.gplay/config.json` project-pin cascade for it (that pin is the repo's own
// app); resolution is deliberately non-interactive and CI-friendly:
//
//	--store-package  →  $GPLAY_APP_STORE_PACKAGE
//
// Later layers win, mirroring ADR-0004 (see ADR-0043). An unresolved value is
// CLI misuse (exit 2) naming both ways to set it.
package appstorecmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// FlagStorePackage is the flag name carrying the app store package name. Named
// once here so every leaf in the namespace spells it identically.
const FlagStorePackage = "store-package"

// EnvStorePackage supplies the app store package name from the environment
// (ADR-0043). It sits below the --store-package flag in the cascade so a CI job
// can export it once and every `gplay appstore` command inherits it.
const EnvStorePackage = "GPLAY_APP_STORE_PACKAGE"

// usageError is a CLI-misuse error with ExitCode()=2 (docs/DESIGN.md §9).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Usagef builds a usage error (exit 2) for the leaves to share.
func Usagef(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

// RegisterStorePackageFlag declares --store-package on a leaf. Every `appstore`
// leaf calls this rather than spelling the flag itself, so the name and usage
// string stay defined in one place.
func RegisterStorePackageFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, FlagStorePackage, "", "package name of the third-party app store making the call (falls back to $"+EnvStorePackage+")")
}

// ResolveStorePackage resolves the app store package name from the
// --store-package flag, falling back to $GPLAY_APP_STORE_PACKAGE. Nothing is
// ever prompted for: an unresolved value is a usage error (exit 2) naming both
// layers, so a CI run fails fast and legibly.
func ResolveStorePackage(flag string) (string, error) {
	if v := strings.TrimSpace(flag); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv(EnvStorePackage)); v != "" {
		return v, nil
	}
	return "", &usageError{msg: "no app store package name — pass --" + FlagStorePackage + " <pkg> or export " + EnvStorePackage +
		" (the package name of the app store on whose behalf the request is made, not the app being read or acted on)"}
}

// RequirePlayPackage validates a required Play app package name, returning a
// usage error (exit 2) carrying usage when it is empty or whitespace-only.
func RequirePlayPackage(pkg, usage string) (string, error) {
	if pkg = strings.TrimSpace(pkg); pkg == "" {
		return "", &usageError{msg: usage}
	}
	return pkg, nil
}

// ResolvePackage resolves the target app package: --package wins, else the
// project pin. This is the app the store hosts, addressed the same way as
// everywhere else in gplay.
func ResolvePackage(rc *kernel.RunContext, flag string) (string, error) {
	pkg := strings.TrimSpace(flag)
	if pkg == "" && rc != nil && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return "", &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}
	return pkg, nil
}

// ParseRFC3339 validates a required RFC 3339 timestamp flag (the `google-datetime`
// format the API's time-range parameters take). It returns the parsed instant —
// for range checks — and the trimmed original string, which is what travels on
// the wire so the caller's offset is preserved verbatim. A missing or malformed
// value is CLI misuse (exit 2) naming the flag and a valid example, so a CI run
// fails fast instead of paying for a round trip that the API would reject.
func ParseRFC3339(flag, value string) (time.Time, string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Time{}, "", &usageError{msg: "missing --" + flag + ": an RFC 3339 timestamp is required, e.g. --" + flag + " 2026-07-01T00:00:00Z"}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, "", &usageError{msg: fmt.Sprintf("invalid --%s %q: expected an RFC 3339 timestamp, e.g. 2026-07-01T00:00:00Z or 2026-07-01T02:00:00+02:00", flag, v)}
	}
	return t, v, nil
}

// ValidateTimeRange validates the required --start-time / --end-time pair and
// returns the two strings to send. The range is [start, end) — the API documents
// the start as inclusive and the end as exclusive — so an end at or before the
// start can only ever return nothing and is rejected as CLI misuse (exit 2).
func ValidateTimeRange(startFlag, endFlag string) (string, string, error) {
	start, startStr, err := ParseRFC3339("start-time", startFlag)
	if err != nil {
		return "", "", err
	}
	end, endStr, err := ParseRFC3339("end-time", endFlag)
	if err != nil {
		return "", "", err
	}
	if !end.After(start) {
		return "", "", &usageError{msg: fmt.Sprintf("invalid time range: --end-time %q must be after --start-time %q (the range is [start, end), end exclusive)", endStr, startStr)}
	}
	return startStr, endStr, nil
}

// forbiddenError wraps a 403 with an agent-resolvable hint: the catalog export
// is granted per app store, so the usual fix is an enrollment/allow-list gap on
// the app store package name, not a per-app permission. It carries no ExitCode
// of its own so the wrapped *api.Error (403 → exit 11) stays authoritative.
type forbiddenError struct {
	storePkg string
	cause    error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account cannot read the Play catalog export on behalf of app store %q — verify --store-package names an app store enrolled for the Catalog Export and that the credential is authorized for it: %v", e.storePkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// notFoundError wraps a 404 on the catalog app view: either the app store
// package name is unknown or the Play app is not eligible for catalog
// inclusion. The wrapped *api.Error drives the exit code (404 → 30).
type notFoundError struct {
	storePkg string
	playPkg  string
	cause    error
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("no catalog app view for Play app %q (app store %q) — the app may not be eligible for catalog inclusion, may have been removed from the Play Store, or the app store package name may be wrong: %v", e.playPkg, e.storePkg, e.cause)
}
func (e *notFoundError) Unwrap() error { return e.cause }

// ClassifyAppView adds 403/404 hints to a catalog-app-view read failure,
// leaving the *api.Error to drive the exit code (403 → 11, 404 → 30). Any other
// error passes through.
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

// storeNotFoundError wraps a 404 on a store-scoped read: there is no Play app in
// play here, only the app store package name, so the hint names that alone.
type storeNotFoundError struct {
	storePkg string
	cause    error
}

func (e *storeNotFoundError) Error() string {
	return fmt.Sprintf("no catalog export for app store %q — verify --store-package names an app store enrolled for the Google Play Catalog Export: %v", e.storePkg, e.cause)
}
func (e *storeNotFoundError) Unwrap() error { return e.cause }

// ClassifyStoreRead adds 403/404 hints to a read keyed by the app store package
// name alone (no Play app), leaving the *api.Error to drive the exit code
// (403 → 11, 404 → 30). Any other error passes through.
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

// ClassifyHostedApp is ClassifyReview for the calls that act on an app the
// store has ALREADY created — upload, publish-status, update. It differs on
// 404 only: there, a missing hosted app record is at least as likely as a
// wrong store, so the hint names both. Use ClassifyReview for `appstore
// create`, where the record cannot be the thing that is missing.
func ClassifyHostedApp(storePkg, pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return &hostedAppNotFoundError{storePkg: storePkg, pkg: pkg, cause: err}
	}
	return ClassifyReview(storePkg, err)
}

// reviewForbiddenError wraps a 403 on the `appstoreappsreview` write path.
// Access is granted to enrolled third-party app stores, so the refusal names
// both halves of what has to be true. No ExitCode of its own — the wrapped
// *api.Error (403 → exit 11) stays authoritative.
type reviewForbiddenError struct {
	storePkg string
	cause    error
}

func (e *reviewForbiddenError) Error() string {
	return fmt.Sprintf("service account cannot act as app store %q — the app store package name must be one Google has enrolled for alternative distribution, and the service account must be linked to that store's Developer account, then retry: %v", e.storePkg, e.cause)
}
func (e *reviewForbiddenError) Unwrap() error { return e.cause }

// reviewStoreNotFoundError wraps a 404 on the app store path key of a review
// write with an actionable hint. The wrapped *api.Error drives the exit code
// (404 → exit 30).
type reviewStoreNotFoundError struct {
	storePkg string
	cause    error
}

func (e *reviewStoreNotFoundError) Error() string {
	return fmt.Sprintf("app store %q not found — verify the --%s value names the third-party app store's own package name (not the hosted app's): %v", e.storePkg, FlagStorePackage, e.cause)
}
func (e *reviewStoreNotFoundError) Unwrap() error { return e.cause }

// hostedAppNotFoundError wraps a 404 on a call that addresses an EXISTING
// hosted app (upload, publish-status, update) rather than creating one. Two
// things can be missing there, and only one of them is the store: Google's own
// contract is that `createappstorehostedapp` "must be called before any other
// RPCs for this hosted app", so a caller who skipped `appstore create` lands
// here. Naming only the store would send them auditing a --store-package that
// is very likely correct. The wrapped *api.Error drives the exit code
// (404 → exit 30).
type hostedAppNotFoundError struct {
	storePkg string
	pkg      string
	cause    error
}

func (e *hostedAppNotFoundError) Error() string {
	return fmt.Sprintf("no hosted app %q in app store %q — run `gplay appstore create --%s %s --package %s` first (it is the mandatory precondition for every other call here), or verify the --%s value names the app store's own package name (not the hosted app's): %v",
		e.pkg, e.storePkg, FlagStorePackage, e.storePkg, e.pkg, FlagStorePackage, e.cause)
}
func (e *hostedAppNotFoundError) Unwrap() error { return e.cause }

// ClassifyReview adds 403/404 hints to an `appstoreappsreview` write failure,
// leaving the *api.Error to drive the exit code (403 → 11, 404 → 30). Any other
// error passes through unchanged.
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
