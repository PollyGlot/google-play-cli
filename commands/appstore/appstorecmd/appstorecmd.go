// Package appstorecmd holds the wiring shared by the `gplay appstore` leaves:
// the exit-2 usage error, resolution of the two addressing values every leaf in
// the namespace takes (the app store package name and the app package name),
// and the 403/404 hint classification that turns a bare API rejection into an
// agent-resolvable refusal. Mirrors commands/orders/orderscmd.
//
// The `appstore` namespace wraps `appstoreappsreview` — the surface a
// third-party Android app store uses to submit the apps it hosts to Google for
// review. Its addressing axis is the **app store package name**
// (`appstore/{appStorePackageName}/...`), which is why every leaf carries
// --store-package on top of the usual --package: the caller is the store, the
// subject is the app it hosts. See CONTEXT.md ("App store package name",
// "Hosted app") and PRD #377.
package appstorecmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

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

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// RegisterStorePackageFlag declares --store-package on a leaf. Every `appstore`
// leaf calls this rather than spelling the flag itself, so the name, usage
// string and (later) any project-level default stay defined in one place.
func RegisterStorePackageFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, FlagStorePackage, "", "package name of the third-party app store making the call (falls back to $"+EnvStorePackage+")")
}

// ResolveStorePackage resolves the app store package name — the
// `appstore/{appStorePackageName}` path key — from the --store-package flag,
// falling back to $GPLAY_APP_STORE_PACKAGE (ADR-0043). It identifies the
// *calling store*, not the app, so the project pin (which pins a package, never
// a store — CONTEXT.md "Project") is deliberately not consulted; nothing is
// ever prompted for, an unresolved value is a usage error (exit 2) naming both
// layers so CI fails fast and legibly.
func ResolveStorePackage(flag string) (string, error) {
	if v := strings.TrimSpace(flag); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv(EnvStorePackage)); v != "" {
		return v, nil
	}
	return "", &usageError{msg: "no app store package name — pass --" + FlagStorePackage + " <appStorePackageName> or export " + EnvStorePackage + " (the package name of the third-party app store making the call, not the hosted app)"}
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

// forbiddenError wraps a 403 with an agent-resolvable hint. Access to
// `appstoreappsreview` is granted to enrolled third-party app stores, so a
// refusal names both halves of what has to be true. It carries no ExitCode of
// its own so the wrapped *api.Error (403 → exit 11) stays authoritative.
type forbiddenError struct {
	storePackage string
	cause        error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account cannot act as app store %q — the app store package name must be one Google has enrolled for alternative distribution, and the service account must be linked to that store's Developer account, then retry: %v", e.storePackage, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// storeNotFoundError wraps a 404 on the app store path key with an actionable
// hint. It carries no ExitCode of its own so the wrapped *api.Error (404 → exit
// 30) stays authoritative.
type storeNotFoundError struct {
	storePackage string
	cause        error
}

func (e *storeNotFoundError) Error() string {
	return fmt.Sprintf("app store %q not found — verify the --%s value names the third-party app store's own package name (not the hosted app's): %v", e.storePackage, FlagStorePackage, e.cause)
}
func (e *storeNotFoundError) Unwrap() error { return e.cause }

// Classify adds 403/404 hints to an `appstore` failure, leaving the *api.Error
// to drive the exit code (403 → 11, 404 → 30). Any other error passes through
// unchanged.
func Classify(storePackage string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{storePackage: storePackage, cause: err}
		case http.StatusNotFound:
			return &storeNotFoundError{storePackage: storePackage, cause: err}
		}
	}
	return err
}
