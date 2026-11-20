package set_test

import (
	"testing"

	publishstatus "github.com/PollyGlot/google-play-cli/commands/appstore/publishstatus/set"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := publishstatus.Payload{StorePackage: "com.example.store", Package: "com.example.app", Word: "published", State: "PUBLISHED", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := publishstatus.Payload{StorePackage: "com.example.store", Package: "com.example.app", Word: "unpublished", State: "UNPUBLISHED"}
	outputtest.GoldenJSON(t, "updated_empty_body.json.golden", p)
}
