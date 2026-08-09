// Package appstorecmd holds the wiring shared by every `gplay appstore` leaf:
// app store package name resolution, the exit-2 usage error, and the 403/404
// hint classification that turns a bare API rejection into an agent-resolvable
// refusal. Mirrors commands/orders/orderscmd and commands/games/gamescmd, and
// keeps the leaves thin.
//
// Addressing rides the APP STORE PACKAGE NAME — the package of the alternative
// app store on whose behalf the request is made — which is a different axis
// from the Android package the rest of gplay targets: it names the *caller's
// store*, not the app being read. There is therefore no `.gplay/config.json`
// project-pin cascade (that pin is the repo's own app); resolution is
// deliberately non-interactive and CI-friendly:
//
//	--store-package  →  $GPLAY_APP_STORE_PACKAGE
//
// Later layers win, mirroring ADR-0004. An unresolved value is CLI misuse
// (exit 2) naming both ways to set it.
package appstorecmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// EnvStorePackage supplies the app store package name from the environment. It
// sits below the --store-package flag in the cascade so a CI job can export it
// once and every `gplay appstore` command inherits it.
const EnvStorePackage = "GPLAY_APP_STORE_PACKAGE"

// usageError is a CLI-misuse error with ExitCode()=2 (docs/DESIGN.md §9).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Usagef builds a usage error (exit 2) for the leaves to share.
func Usagef(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

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
	return "", &usageError{msg: "no app store package name — pass --store-package <pkg> or export " + EnvStorePackage +
		" (the package name of the app store on whose behalf the request is made, not the app being read)"}
}

// RequirePlayPackage validates a required Play app package name, returning a
// usage error (exit 2) carrying usage when it is empty or whitespace-only.
func RequirePlayPackage(pkg, usage string) (string, error) {
	if pkg = strings.TrimSpace(pkg); pkg == "" {
		return "", &usageError{msg: usage}
	}
	return pkg, nil
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
