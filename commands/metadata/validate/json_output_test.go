package validatecmd_test

import (
	"testing"

	validatecmd "github.com/PollyGlot/google-play-cli/commands/metadata/validate"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_ok_golden freezes the offline success object a pre-commit
// hook reads: the linted locales in sorted order and ok:true. The dir echoes
// the operator's --dir verbatim, so it carries an & that must stay unescaped.
func TestRenderJSON_ok_golden(t *testing.T) {
	p := validatecmd.Payload{Dir: "./R&D/metadata", Locales: []string{"de-DE", "en-US", "fr-FR"}, OK: true}
	outputtest.GoldenJSON(t, "ok.json.golden", p)
}
