package status_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/edits/editscmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

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
		{"status_live_gone.json.golden", editscmd.Payload{Package: "com.example.app", EditID: "edit-4f2a", Open: false, Live: true, Gone: true}},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			outputtest.GoldenJSON(t, c.golden, c.p)
		})
	}
}
