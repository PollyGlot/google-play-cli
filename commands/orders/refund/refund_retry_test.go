package refund_test

import (
	"testing"

	refundcmd "github.com/PollyGlot/google-play-cli/commands/orders/refund"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// TestRun_refund_retryNeverReplaysTheRefund drives the real kernel wiring of
// --retry (#575): a 503 on orders.refund may come after Google moved the
// money, so even with --retry 3 the POST is sent exactly once and the 503 is
// handed back as exit 40 for the operator to check the order, not replayed.
func TestRun_refund_retryNeverReplaysTheRefund(t *testing.T) {
	rt := &refundRT{status: 503, body: `{"error":{"code":503,"message":"backend unavailable"}}`}
	rc := newRC(t, rt)
	rc.Retry = 3
	_, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", Confirm: true})
	if code := exit.For(err); code != 40 {
		t.Fatalf("exit = %d (err %v), want 40", code, err)
	}
	refunds := 0
	for _, c := range rt.calls {
		if c != "POST /token" {
			refunds++
		}
	}
	if refunds != 1 {
		t.Errorf("orders.refund sent %d times under --retry 3, want exactly 1: %v", refunds, rt.calls)
	}
}
