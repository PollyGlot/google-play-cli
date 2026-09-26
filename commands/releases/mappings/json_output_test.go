package mappings_test

import (
	"bytes"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/mappings"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// mappingUploadBody is a deobfuscationfiles.upload response in a layout gplay
// would not produce itself: passthrough means these exact bytes reach stdout.
// The resource has no free-text field, so the layout (not &, <, >) is what a
// re-encoding would visibly change.
const mappingUploadBody = `{
 "deobfuscationFile": {
  "symbolType": "proguard"
 }
}`

// TestRenderJSON_passthrough_isTheUploadBodyVerbatim asserts `--output json`
// prints the deobfuscationfiles.upload body byte for byte (ADR-0003). The
// Result is built directly: the package fake answers the resumable upload
// with a fixed body, so it cannot carry these bytes.
func TestRenderJSON_passthrough_isTheUploadBodyVerbatim(t *testing.T) {
	p := mappings.Payload{Result: &orchestrator.MappingResult{
		VersionCode: 142,
		FileType:    "proguard",
		SymbolType:  "proguard",
		Raw:         []byte(mappingUploadBody),
	}}
	if got := outputtest.RenderJSON(t, p); !bytes.Equal(got, []byte(mappingUploadBody)) {
		t.Errorf("JSON output is not the upload body verbatim\n got: %s\nwant: %s", got, mappingUploadBody)
	}
}

// TestRenderJSON_dryRun_golden freezes the gplay MappingResult shape --dry-run
// prints: no upload ran, so there is no API body to pass through.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	rt := &mappingRT{t: t}
	rc := newRC(t, rt)

	r, err := mappings.Run(rc, mappings.Input{Package: "com.example.app", MappingPath: writeFakeMapping(t), VersionCode: 142, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Fatalf("dry-run hit the network: %v", rt.calls)
	}
	outputtest.GoldenJSON(t, "dry_run.json.golden", r)
}
