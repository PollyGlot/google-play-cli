package list_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/team/grants/list"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_grants_golden freezes the {"grants":[...]} projection. The
// second row has nil permissions: it must print [] so a consumer never sees null.
func TestRenderJSON_grants_golden(t *testing.T) {
	p := list.Payload{Rows: []list.GrantRow{
		{Email: "dev@example.com", Package: "com.example.app", Permissions: []string{"CAN_REPLY_TO_REVIEWS", "CAN_VIEW_APP_QUALITY"}},
		{Email: "qa@example.com", Package: "com.example.app.beta"},
	}}
	outputtest.GoldenJSON(t, "grants.json.golden", p)
}

// TestRenderJSON_noGrants_golden freezes the empty result: an array, not null,
// so `jq '.grants[]'` stays valid on an account with no grants.
func TestRenderJSON_noGrants_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "no_grants.json.golden", list.Payload{})
}
