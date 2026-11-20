package mappings_test

import (
	"bytes"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/mappings"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

const mappingUploadBody = `{
 "deobfuscationFile": {
  "symbolType": "proguard"
 }
}`

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

func TestRenderJSON_dryRun_golden(t *testing.T) {
	rt, transport := newMappingTransport("")
	rc := newRC(t, transport)

	r, err := mappings.Run(rc, mappings.Input{Package: "com.example.app", MappingPath: writeFakeMapping(t), VersionCode: 142, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if touched(rt) {
		t.Fatalf("dry-run hit the network: %v", apiCalls(rt))
	}
	outputtest.GoldenJSON(t, "dry_run.json.golden", r)
}
