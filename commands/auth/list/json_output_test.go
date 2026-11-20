package list_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/auth/list"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_accounts_golden(t *testing.T) {
	p := list.Payload{Accounts: []list.AccountRow{
		{Name: "ci", Active: true},
		{Name: "personal", Active: false},
	}}
	outputtest.GoldenJSON(t, "accounts.json.golden", p)
}

func TestRenderJSON_empty_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "empty.json.golden", list.Payload{Accounts: []list.AccountRow{}})
}
