package status_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/edits/editscmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_editsEnvelope_golden freezes the shared editscmd.Payload
// envelope that begin/commit/discard/status print. It lives here because
// editscmd has no leaf of its own; each case is one state the leaves emit,
// and omitempty decides which keys appear, so every state gets its golden.
func TestRenderJSON_editsEnvelope_golden(t *testing.T) {
	cases := []struct {
		golden string
		p      editscmd.Payload
	}{
		{"began.json.golden", editscmd.Payload{Package: "com.example.app", EditID: "edit-4f2a", Open: true, Action: "began"}},
		{"committed.json.golden", editscmd.Payload{Package: "com.example.app", EditID: "edit-4f2a", Open: false, Action: "committed"}},
		{"discarded.json.golden", editscmd.Payload{Package: "com.example.app", EditID: "edit-4f2a", Open: false, Action: "discarded"}},
		{"status_open.json.golden", editscmd.Payload{Package: "com.example.app", EditID: "edit-4f2a", Open: true}},
		{"status_closed.json.golden", editscmd.Payload{Package: "com.example.app"}},
		{"status_live_open.json.golden", editscmd.Payload{Package: "com.example.app", EditID: "edit-4f2a", Open: true, Live: true, ExpiryTimeSeconds: "1700000000"}},
		// Gone is json:"-": open:false with live:true and the pinned editId
		// is how JSON says the server no longer knows the Edit.
		{"status_live_gone.json.golden", editscmd.Payload{Package: "com.example.app", EditID: "edit-4f2a", Open: false, Live: true, Gone: true}},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			outputtest.GoldenJSON(t, c.golden, c.p)
		})
	}
}
