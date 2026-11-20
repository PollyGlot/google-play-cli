package permissions_test

import (
	"testing"

	permscmd "github.com/PollyGlot/google-play-cli/commands/team/permissions"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

func TestRenderJSON_accountScope_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "scope_account.json.golden", permscmd.Payload{Scope: vocab.Account})
}

func TestRenderJSON_appScope_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "scope_app.json.golden", permscmd.Payload{Scope: vocab.App})
}
