package listcmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/listcmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_golden(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{
		{Package: "com.example.app", Pinned: true},
		{Package: "com.example.app.beta", Pinned: false},
	}}
	outputtest.GoldenJSON(t, "list.json.golden", p)
}

func TestRenderJSON_empty_golden(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{}}
	outputtest.GoldenJSON(t, "empty.json.golden", p)
}
