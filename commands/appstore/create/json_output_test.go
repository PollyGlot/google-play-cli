package create_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/appstore/create"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the --dry-run preview, whose empty
// `requires` array must print as [] rather than null.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := create.Payload{StorePackage: "com.example.store", Package: "com.example.app", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

// TestRenderJSON_emptyBody_golden freezes the success object that stands in
// when the API answers with no body; a non-empty body passes through instead.
func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := create.Payload{StorePackage: "com.example.store", Package: "com.example.app"}
	outputtest.GoldenJSON(t, "created_empty_body.json.golden", p)
}
