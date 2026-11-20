package migrate_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/subscriptions/prices/migrate"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := migrate.Payload{
		Product:  "premium_monthly",
		BasePlan: "monthly-autorenew",
		Regions:  []string{"FR", "US"},
		Oldest:   "2026-01-01T00:00:00Z",
		Requires: []string{"confirm"},
		DryRun:   true,
	}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

func TestRenderJSON_dryRun_requiresNeverNull(t *testing.T) {
	got := outputtest.RenderJSON(t, migrate.Payload{Product: "premium_monthly", BasePlan: "monthly-autorenew", Regions: []string{"FR"}, DryRun: true})
	if !bytes.Contains(got, []byte(`"requires": []`)) {
		t.Errorf("requires must encode as [], got %s", got)
	}
}

func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := migrate.Payload{
		Product:  "premium_monthly",
		BasePlan: "monthly-autorenew",
		Regions:  []string{"FR", "US"},
		Oldest:   "2026-01-01T00:00:00Z",
		Raw:      json.RawMessage(`{}`),
	}
	outputtest.GoldenJSON(t, "empty_body.json.golden", p)
}
