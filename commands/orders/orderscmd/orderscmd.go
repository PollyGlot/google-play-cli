package orderscmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const PermViewFinancialData = "CAN_VIEW_FINANCIAL_DATA"

const PermManageOrders = "CAN_MANAGE_ORDERS"

type orderNotFoundError struct {
	orderID string
	cause   error
}

func (e *orderNotFoundError) Error() string {
	return fmt.Sprintf("order %q not found: verify the order ID from the buyer's receipt or a payout report, and that --package names the app the order belongs to: %v", e.orderID, e.cause)
}
func (e *orderNotFoundError) Unwrap() error { return e.cause }

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account cannot read orders for %q: grant it the %s permission (Play Console → Users & permissions; it is never part of a Role bundle), then retry: %v", e.pkg, PermViewFinancialData, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

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

func ClassifyList(pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return &forbiddenError{pkg: pkg, cause: err}
	}
	return err
}

type batchNotFoundError struct {
	count int
	cause error
}

func (e *batchNotFoundError) Error() string {
	return fmt.Sprintf("one or more of the %d order IDs were not found: orders.batchget fails the whole request if any ID is unknown or belongs to another package; verify every ID and that --package names the app they belong to: %v", e.count, e.cause)
}
func (e *batchNotFoundError) Unwrap() error { return e.cause }

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

type refundForbiddenError struct {
	pkg   string
	cause error
}

func (e *refundForbiddenError) Error() string {
	return fmt.Sprintf("service account cannot refund orders for %q: grant it the %s permission (Play Console → Users & permissions; it is never part of a Role bundle), then retry: %v", e.pkg, PermManageOrders, e.cause)
}
func (e *refundForbiddenError) Unwrap() error { return e.cause }

type refundTooOldError struct {
	orderID string
	cause   error
}

func (e *refundTooOldError) Error() string {
	return fmt.Sprintf("order %q cannot be refunded: Google does not allow refunding orders older than 3 years (this is a hard API limit, not a permission issue): %v", e.orderID, e.cause)
}
func (e *refundTooOldError) Unwrap() error { return e.cause }

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

func mentionsRefundTooOld(e *api.Error) bool {
	hay := strings.ToLower(e.Message + " " + strings.Join(e.Reasons, " "))
	for _, frag := range []string{"3 year", "three year", "older than 3", "older than three"} {
		if strings.Contains(hay, frag) {
			return true
		}
	}
	return false
}
