package signingcmd_test

import (
	"bytes"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/signing/signingcmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := signingcmd.Payload{Verb: "rotate", Package: "com.example.app", Requires: []string{"confirm"}, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

func TestRenderJSON_dryRun_requiresNeverNull(t *testing.T) {
	got := outputtest.RenderJSON(t, signingcmd.Payload{Verb: "enroll", Package: "com.example.app", DryRun: true})
	if !bytes.Contains(got, []byte(`"requires": []`)) {
		t.Errorf("requires must encode as [], got %s", got)
	}
}
