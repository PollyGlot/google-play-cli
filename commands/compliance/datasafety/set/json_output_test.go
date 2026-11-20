package set_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/compliance/datasafety/set"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

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

func TestRenderJSON_dryRunInlineCredential_golden(t *testing.T) {
	p := set.Payload{Package: "com.example.app", Bytes: 2048, Rows: 42, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run_inline_credential.json.golden", p)
}

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
