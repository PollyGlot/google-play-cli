package validate_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/edits/editscmd"
	"github.com/PollyGlot/google-play-cli/commands/edits/validate"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_emptyRawResponse_fallsBackToEditsEnvelope(t *testing.T) {
	p := validate.Payload{
		Payload: editscmd.Payload{Package: "com.example.app", EditID: "edit-4f2a", Open: true, Action: "validated"},
	}
	outputtest.GoldenJSON(t, "fallback.json.golden", p)
}
