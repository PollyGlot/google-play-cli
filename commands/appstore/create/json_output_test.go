package create_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/appstore/create"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := create.Payload{StorePackage: "com.example.store", Package: "com.example.app", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := create.Payload{StorePackage: "com.example.store", Package: "com.example.app"}
	outputtest.GoldenJSON(t, "created_empty_body.json.golden", p)
}
