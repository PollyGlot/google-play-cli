package refund_test

import (
	"bytes"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/orders/refund"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := refund.Payload{OrderID: "GPA.1234-5678-9012-34567", Revoke: true, Requires: []string{"confirm"}, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

func TestRenderJSON_dryRun_requiresNeverNull(t *testing.T) {
	got := outputtest.RenderJSON(t, refund.Payload{OrderID: "GPA.1234-5678-9012-34567", DryRun: true})
	if !bytes.Contains(got, []byte(`"requires": []`)) {
		t.Errorf("requires must encode as [], got %s", got)
	}
}

func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := refund.Payload{OrderID: "GPA.1234-5678-9012-34567"}
	outputtest.GoldenJSON(t, "refunded_empty_body.json.golden", p)
}
