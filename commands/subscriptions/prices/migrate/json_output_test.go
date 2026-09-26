package migrate_test

import (
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/subscriptions/prices/migrate"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the money-moving preview: the target,
// the cohort cutoff and the "requires" gate an agent reads before re-running
// with --confirm (ADR-0017).
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

// TestRenderJSON_emptyBody_golden freezes the success object gplay prints when
// migratePrices answers with a bare {} (its usual reply): no body worth
// passing through, so a CI parser still gets ok:true and the target.
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
