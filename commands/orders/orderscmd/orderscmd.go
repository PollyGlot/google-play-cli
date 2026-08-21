// Package orderscmd holds the wiring shared by the `gplay orders` leaves:
// package resolution, the exit-2 usage error, and the 404/403 hint
// classification that turns a bare API rejection into an agent-resolvable
// refusal. The read leaves (view single / batch) name CAN_VIEW_FINANCIAL_DATA
// on a 403; the refund leaf names CAN_MANAGE_ORDERS and surfaces the API's
// "orders older than 3 years cannot be refunded" rule as a specific refusal.
// Mirrors commands/releases/generated/generatedcmd. See ADR-0031 /
// CONTEXT.md ("Order").
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

// PermManageOrders is the Play permission required to refund an order
// (orders.refund). Like PermViewFinancialData it is a money capability, never
// part of a Role bundle (ADR-0016), so a refusal names it verbatim.
const PermManageOrders = "CAN_MANAGE_ORDERS"

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
		return "", &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
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
	return fmt.Sprintf("order %q not found: verify the order ID from the buyer's receipt or a payout report, and that --package names the app the order belongs to: %v", e.orderID, e.cause)
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
	return fmt.Sprintf("service account cannot read orders for %q: grant it the %s permission (Play Console → Users & permissions; it is never part of a Role bundle), then retry: %v", e.pkg, PermViewFinancialData, e.cause)
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

// batchNotFoundError wraps a 404 on a batch read: orders.batchget fails the
// whole request if any single ID is unknown or belongs to another package, so
// the hint names that all-or-nothing semantics rather than a single ID.
type batchNotFoundError struct {
	count int
	cause error
}

func (e *batchNotFoundError) Error() string {
	return fmt.Sprintf("one or more of the %d order IDs were not found: orders.batchget fails the whole request if any ID is unknown or belongs to another package; verify every ID and that --package names the app they belong to: %v", e.count, e.cause)
}
func (e *batchNotFoundError) Unwrap() error { return e.cause }

// ClassifyBatchView is ClassifyView for the batch read: the 403 hint is shared
// (CAN_VIEW_FINANCIAL_DATA), but a 404 reflects batchget's all-or-nothing
// semantics over the whole ID list. count is how many IDs were requested.
func ClassifyBatchView(pkg string, count int, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return &batchNotFoundError{count: count, cause: err}
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		}
	}
	return err
}

// refundForbiddenError wraps a 403 on a refund with an agent-resolvable hint
// naming the missing money-management permission.
type refundForbiddenError struct {
	pkg   string
	cause error
}

func (e *refundForbiddenError) Error() string {
	return fmt.Sprintf("service account cannot refund orders for %q: grant it the %s permission (Play Console → Users & permissions; it is never part of a Role bundle), then retry: %v", e.pkg, PermManageOrders, e.cause)
}
func (e *refundForbiddenError) Unwrap() error { return e.cause }

// refundTooOldError wraps the API's "orders older than 3 years cannot be
// refunded" rejection (orders.refund Discovery description) into a specific,
// agent-readable refusal rather than a generic 4xx. It carries no ExitCode of
// its own, so the wrapped *api.Error stays authoritative (the API returns 400
// → exit 30).
type refundTooOldError struct {
	orderID string
	cause   error
}

func (e *refundTooOldError) Error() string {
	return fmt.Sprintf("order %q cannot be refunded: Google does not allow refunding orders older than 3 years (this is a hard API limit, not a permission issue): %v", e.orderID, e.cause)
}
func (e *refundTooOldError) Unwrap() error { return e.cause }

// ClassifyRefund adds 403/404/too-old hints to a refund failure, leaving the
// *api.Error to drive the exit code (403 → 11, 404 → 30, 400 → 30). A 4xx whose
// message mentions the 3-year limit becomes the specific too-old refusal; any
// other error passes through.
func ClassifyRefund(pkg, orderID string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &refundForbiddenError{pkg: pkg, cause: err}
		case http.StatusNotFound:
			return &orderNotFoundError{orderID: orderID, cause: err}
		default:
			if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && mentionsRefundTooOld(apiErr) {
				return &refundTooOldError{orderID: orderID, cause: err}
			}
		}
	}
	return err
}

// mentionsRefundTooOld reports whether an API error is the older-than-3-years
// refund rejection.
//
// CAVEAT: the exact runtime wording is Google's and is NOT captured in the
// Discovery snapshot: only the method *description* states "Orders older than
// 3 years cannot be refunded", which is documentation, not the error-envelope
// `message` the API returns at refund time. So we scan both the human message
// and the structured reason codes (api.Error.Reasons) for the stable fragments
// case-insensitively, and this should be revisited once a real 4xx body is
// observed. Erring narrow is safe: a miss falls through to the generic
// *api.Error (still the correct exit 30), just without the specific hint;
// erring broad is not a concern here because the only other plausible refund
// 4xx (403/404) are already routed before this branch.
func mentionsRefundTooOld(e *api.Error) bool {
	hay := strings.ToLower(e.Message + " " + strings.Join(e.Reasons, " "))
	for _, frag := range []string{"3 year", "three year", "older than 3", "older than three"} {
		if strings.Contains(hay, frag) {
			return true
		}
	}
	return false
}
