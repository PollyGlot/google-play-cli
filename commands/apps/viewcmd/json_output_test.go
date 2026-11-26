package viewcmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/viewcmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

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
