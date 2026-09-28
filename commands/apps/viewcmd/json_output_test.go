package viewcmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/viewcmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_envelope_golden freezes the {"details","listing","icon"}
// envelope: gplay-authored (an explicit ADR-0003 exception, several endpoints
// merged), so its key order and the icon key are ours to keep stable. It is
// built inside details.Get, hence the drive through Run on the fake API.
// The title carries &, < and >: the envelope is built with output.Marshal, so
// the listing bytes reach stdout unescaped.
func TestRenderJSON_envelope_golden(t *testing.T) {
	api := &viewAPI{
		editID:  "edit-view",
		details: `{"contactEmail":"dev@example.com","defaultLanguage":"en-US"}`,
		listing: `{"language":"en-US","title":"Example <Notes> & Tasks"}`,
		icon:    `{"images":[{"id":"ic1","url":"https://play.example/icon.png","sha1":"d1","sha256":"3f1e0c2a"}]}`,
	}
	rc, _ := newRC(t, api.serve(t))

	r, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	outputtest.GoldenJSON(t, "envelope.json.golden", r)
}

// TestRenderJSON_emptyRawResponse_fallsBackToPayloadShape pins the silent
// fallback: a Payload that lost its envelope renders the typed trio rather
// than nothing, through output.WriteJSON (no HTML escaping of the title).
func TestRenderJSON_emptyRawResponse_fallsBackToPayloadShape(t *testing.T) {
	p := viewcmd.Payload{
		Package:         "com.example.app",
		DefaultLanguage: "en-US",
		Title:           "Example <Notes> & Tasks",
		ContactEmail:    "dev@example.com",
		IconSha256:      "3f1e0c2a",
	}
	outputtest.GoldenJSON(t, "fallback.json.golden", p)
}
