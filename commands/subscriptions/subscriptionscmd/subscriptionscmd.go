// Package subscriptionscmd holds the wiring shared by the `gplay
// subscriptions` leaves: package resolution, the default catalog directory,
// and the 403/404 hint classification that turns a bare API rejection into an
// agent-resolvable refusal. Mirrors commands/orders/orderscmd. See ADR-0041 /
// CONTEXT.md ("Subscription", "Monetization catalog").
package subscriptionscmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// DefaultDir is the catalog directory the subscriptions leaves default to. The
// subscriptions catalog gets its own segment under ./monetization so the
// one-time-product catalog (`iap`, slices #371–#372) can sit beside it without
// the two file sets interleaving.
const DefaultDir = "./monetization/subscriptions"

// DefaultRegionsVersion is the regions version pin sent with subscription
// writes (create/patch) unless --regions-version overrides it — the latest
// version Google has published (ADR-0041 §6).
const DefaultRegionsVersion = "2022/02"

// ResolvePackage resolves the target package: --package wins, else the project
// pin. The Monetization catalog rides the package/app axis like
// releases/metadata/orders.
func ResolvePackage(rc *kernel.RunContext, flag string) (string, error) {
	pkg := strings.TrimSpace(flag)
	if pkg == "" && rc != nil && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return "", exit.Usagef("no package — pass --package <pkg> or run gplay init in your repo")
	}
	return pkg, nil
}

// forbiddenError wraps a 403 with an agent-resolvable hint. The Discovery
// snapshot ties no specific Play permission to the monetization.subscriptions
// methods, so the hint points at the Users & permissions surface without
// naming an enum value it cannot vouch for. It carries no ExitCode of its own,
// so the wrapped *api.Error (403 → exit 11) stays authoritative.
type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account cannot manage the subscription catalog for %q — grant it access to the app's monetization setup in Play Console (Users & permissions), then retry: %v", e.pkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// notFoundError wraps a 404 on the collection: the package itself is unknown
// to the credential (there is no per-product 404 in a list-driven flow that
// isn't already a plan delete).
type notFoundError struct {
	pkg   string
	cause error
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("package %q not found — verify --package (or the project pin) names an app this service account can reach: %v", e.pkg, e.cause)
}
func (e *notFoundError) Unwrap() error { return e.cause }

// Classify adds 403/404 hints to an API failure, leaving the *api.Error to
// drive the exit code (403 → 11, 404 → 30). Any other error passes through.
func Classify(pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		case http.StatusNotFound:
			return &notFoundError{pkg: pkg, cause: err}
		}
	}
	return err
}
