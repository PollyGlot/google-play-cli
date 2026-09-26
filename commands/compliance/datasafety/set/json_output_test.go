package set_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/compliance/datasafety/set"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the rehearsal report: dryRun:true makes
// it unmistakable from an API body, and the free-text warning keeps its < > &
// unescaped on stdout.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := set.Payload{
		Package:  "com.example.app",
		Account:  "ci",
		Bytes:    2048,
		Rows:     42,
		DryRun:   true,
		Warnings: []string{"header differs from the <reference> template & may be stale"},
	}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

// TestRenderJSON_dryRunInlineCredential_golden pins the dry-run with no stored
// Account (inline --service-account): the account key is omitted, not "".
func TestRenderJSON_dryRunInlineCredential_golden(t *testing.T) {
	p := set.Payload{Package: "com.example.app", Bytes: 2048, Rows: 42, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run_inline_credential.json.golden", p)
}

// TestRenderJSON_emptyPostBody_golden freezes the documented ADR-0003
// exception: the dataSafety POST can succeed with no body, so gplay prints its
// own success object rather than an empty stream a CI parser would choke on.
// Warnings and account are deliberately absent from this shape.
func TestRenderJSON_emptyPostBody_golden(t *testing.T) {
	p := set.Payload{
		Package:  "com.example.app",
		Account:  "ci",
		Bytes:    2048,
		Rows:     42,
		Warnings: []string{"header differs from the reference template"},
	}
	outputtest.GoldenJSON(t, "empty_post_body.json.golden", p)
}
