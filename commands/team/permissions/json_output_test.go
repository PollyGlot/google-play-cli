package permissions_test

import (
	"testing"

	permscmd "github.com/PollyGlot/google-play-cli/commands/team/permissions"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

// TestRenderJSON_accountScope_golden freezes the whole vocabulary snapshot as
// `team users` resolves it. The labels carry & natively, so the golden also
// proves they stay unescaped.
func TestRenderJSON_accountScope_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "scope_account.json.golden", permscmd.Payload{Scope: vocab.Account})
}

// TestRenderJSON_appScope_golden freezes the app scope: `enum` switches to the
// bare enum and is omitted for account-only aliases.
func TestRenderJSON_appScope_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "scope_app.json.golden", permscmd.Payload{Scope: vocab.App})
}
