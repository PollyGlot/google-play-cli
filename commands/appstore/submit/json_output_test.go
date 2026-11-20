package submit

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

var sampleSummary = summary{DeveloperName: "Example <Games> & Co", Locales: 2, ApkSets: 1, PolicyDeclarations: 3}

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := Payload{StorePackage: "com.example.store", Package: "com.example.app", Sum: sampleSummary, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := Payload{StorePackage: "com.example.store", Package: "com.example.app", Sum: sampleSummary}
	outputtest.GoldenJSON(t, "submitted_empty_body.json.golden", p)
}
