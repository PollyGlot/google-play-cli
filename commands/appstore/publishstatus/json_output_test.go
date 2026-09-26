package publishstatus_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/appstore/publishstatus"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the --dry-run preview: publishState is
// the API enum sent, not the word the operator typed.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := publishstatus.Payload{StorePackage: "com.example.store", Package: "com.example.app", Word: "published", State: "PUBLISHED", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

// TestRenderJSON_emptyBody_golden freezes the success object that stands in
// when the API answers with no body; a non-empty body passes through instead.
func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := publishstatus.Payload{StorePackage: "com.example.store", Package: "com.example.app", Word: "unpublished", State: "UNPUBLISHED"}
	outputtest.GoldenJSON(t, "updated_empty_body.json.golden", p)
}
