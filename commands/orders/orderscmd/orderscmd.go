// Package orderscmd holds the wiring shared by the `gplay orders` leaves:
// package resolution, the exit-2 usage error, and the 404/403 hint
// classification that turns a bare API rejection into an agent-resolvable
// refusal. The read leaves (view, today; batch view, #283) name
// CAN_VIEW_FINANCIAL_DATA on a 403; the refund leaf (#284) will add its own
// classifier naming CAN_MANAGE_ORDERS. Mirrors commands/releases/generated/
// generatedcmd. See ADR-0031 / CONTEXT.md ("Order").
package orderscmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// PermViewFinancialData is the Play permission a service account must hold to
// read order data (orders.get / orders.batchget). It is never folded into a
// Role bundle (ADR-0016), so a refusal names it verbatim.
const PermViewFinancialData = "CAN_VIEW_FINANCIAL_DATA"

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Usagef builds a usage error (exit 2).
func Usagef(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

// ResolvePackage resolves the target package: --package wins, else the project
// pin. orders ride the package/app axis like releases/metadata (ADR-0031).
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

// orderNotFoundError wraps a 404 with an actionable hint, leaving the wrapped
// *api.Error to drive the exit code (404 → 30).
type orderNotFoundError struct {
	orderID string
	cause   error
}

func (e *orderNotFoundError) Error() string {
	return fmt.Sprintf("order %q not found — verify the order ID from the buyer's receipt or a payout report, and that --package names the app the order belongs to: %v", e.orderID, e.cause)
}
func (e *orderNotFoundError) Unwrap() error { return e.cause }

// forbiddenError wraps a 403 on a read with an agent-resolvable hint naming the
// missing permission. It carries no ExitCode of its own so the wrapped
// *api.Error (403 → exit 11) stays authoritative.
type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account cannot read orders for %q — grant it the %s permission (Play Console → Users & permissions; it is never part of a Role bundle), then retry: %v", e.pkg, PermViewFinancialData, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// ClassifyView adds 404/403 hints to a read failure, leaving the *api.Error to
// drive the exit code (404 → 30, 403 → 11). Any other error passes through.
func ClassifyView(pkg, orderID string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return &orderNotFoundError{orderID: orderID, cause: err}
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		}
	}
	return err
}
