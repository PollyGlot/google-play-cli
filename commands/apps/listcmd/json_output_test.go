package listcmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/listcmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_golden freezes the local-registry listing: a gplay document
// (no API is called), where the pinned row carries "pinned": true.
func TestRenderJSON_golden(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{
		{Package: "com.example.app", Pinned: true},
		{Package: "com.example.app.beta", Pinned: false},
	}}
	outputtest.GoldenJSON(t, "list.json.golden", p)
}

// TestRenderJSON_empty_golden pins the empty registry as {"apps":[]}, never
// null: Run always builds a non-nil slice so a machine consumer can range
// over it without a nil check.
func TestRenderJSON_empty_golden(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{}}
	outputtest.GoldenJSON(t, "empty.json.golden", p)
}
