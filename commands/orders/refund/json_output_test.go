package refund_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/orders/refund"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the --dry-run preview an agent reads to
// learn the destructive gate (ADR-0017 §4) before moving money.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := refund.Payload{OrderID: "GPA.1234-5678-9012-34567", Revoke: true, Requires: []string{"confirm"}, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

// TestRenderJSON_emptyBody_golden freezes the success object that stands in
// for orders.refund's empty body; a non-empty body passes through instead.
func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := refund.Payload{OrderID: "GPA.1234-5678-9012-34567"}
	outputtest.GoldenJSON(t, "refunded_empty_body.json.golden", p)
}
