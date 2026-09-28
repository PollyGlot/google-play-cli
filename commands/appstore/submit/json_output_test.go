package submit

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// sampleSummary carries a developer name with &, < and >: it is the one free
// text field, and output.WriteJSON must print it unescaped.
var sampleSummary = summary{DeveloperName: "Example <Games> & Co", Locales: 2, ApkSets: 1, PolicyDeclarations: 3}

// TestRenderJSON_dryRun_golden freezes the gplay-shaped --dry-run preview
// (internal test: summary is unexported and only Run builds it otherwise).
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := Payload{StorePackage: "com.example.store", Package: "com.example.app", Sum: sampleSummary, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

// TestRenderJSON_emptyBody_golden freezes the success object that stands in
// when the API answers with no body; a non-empty body passes through instead.
func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := Payload{StorePackage: "com.example.store", Package: "com.example.app", Sum: sampleSummary}
	outputtest.GoldenJSON(t, "submitted_empty_body.json.golden", p)
}
