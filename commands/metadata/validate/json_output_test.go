package validatecmd_test

import (
	"testing"

	validatecmd "github.com/PollyGlot/google-play-cli/commands/metadata/validate"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_ok_golden(t *testing.T) {
	p := validatecmd.Payload{Dir: "./R&D/metadata", Locales: []string{"de-DE", "en-US", "fr-FR"}, OK: true}
	outputtest.GoldenJSON(t, "ok.json.golden", p)
}
