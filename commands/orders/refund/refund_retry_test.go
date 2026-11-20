package refund_test

import (
	"testing"

	refundcmd "github.com/PollyGlot/google-play-cli/commands/orders/refund"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestRun_refund_retryNeverReplaysTheRefund(t *testing.T) {
	fake := newFake(503, `{"error":{"code":503,"message":"backend unavailable"}}`)
	rc := newRC(t, fake)
	rc.Retry = 3
	_, err := refundcmd.Run(rc, refundcmd.Input{Package: "com.example.app", OrderID: "GPA.1234", Confirm: true})
	if code := exit.For(err); code != 40 {
		t.Fatalf("exit = %d (err %v), want 40", code, err)
	}
	if calls := fake.Calls(); len(calls) != 1 {
		t.Errorf("orders.refund sent %d times under --retry 3, want exactly 1: %+v", len(calls), calls)
	}
}
