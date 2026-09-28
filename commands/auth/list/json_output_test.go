package list_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/auth/list"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_accounts_golden freezes the {"accounts":[...]} envelope
// with the active marker as a boolean, the shape storedeck reads.
func TestRenderJSON_accounts_golden(t *testing.T) {
	p := list.Payload{Accounts: []list.AccountRow{
		{Name: "ci", Active: true},
		{Name: "personal", Active: false},
	}}
	outputtest.GoldenJSON(t, "accounts.json.golden", p)
}

// TestRenderJSON_empty_golden pins an empty registry to "accounts": [] (Run
// builds a non-nil slice), never null, so consumers can iterate blindly.
func TestRenderJSON_empty_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "empty.json.golden", list.Payload{Accounts: []list.AccountRow{}})
}
