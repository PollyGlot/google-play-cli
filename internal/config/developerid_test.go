package config_test

import (
	"context"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

func TestLoad_activeAccountDeveloperID_isResolved(t *testing.T) {
	f := newLoadFixture(t)
	f.writeGlobal(t, `{"accounts":[{"name":"ci","active":true,"developerId":"4900000000000000000"}]}`)

	r, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.DeveloperID != "4900000000000000000" {
		t.Errorf("DeveloperID = %q, want the active Account's", r.DeveloperID)
	}
}

func TestLoad_projectLocalDeveloperID_overridesActiveAccount(t *testing.T) {
	f := newLoadFixture(t)
	f.writeGlobal(t, `{"accounts":[{"name":"agency","active":true,"developerId":"111"}]}`)
	f.writeLocal(t, `{"developerId":"222"}`)

	r, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.DeveloperID != "222" {
		t.Errorf("DeveloperID = %q, want the project-local override 222", r.DeveloperID)
	}
}

func TestLoad_committedDeveloperID_isRejected(t *testing.T) {
	f := newLoadFixture(t)
	f.writeShared(t, `{"package":"com.example.app","developerId":"4900000000000000000"}`)

	_, err := config.Load(context.Background(), config.OSFS{}, f.loadOpts())
	if err == nil {
		t.Fatal("expected committed developerId to be rejected")
	}
	if !strings.Contains(err.Error(), "developerId") || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("error %q should explain developerId is forbidden in committed config", err.Error())
	}
}

func TestGlobal_developerID_roundTrips(t *testing.T) {
	g := &config.Global{Accounts: []config.Account{{Name: "a", Active: true}}}
	f := newLoadFixture(t)
	if err := g.Save(context.Background(), config.OSFS{}, f.globalPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, f.globalPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if loaded.Accounts[0].DeveloperID != "" {
		t.Errorf("DeveloperID = %q, want empty", loaded.Accounts[0].DeveloperID)
	}

	loaded.Accounts[0].DeveloperID = "999"
	if err := loaded.Save(context.Background(), config.OSFS{}, f.globalPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	again, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, f.globalPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if again.Accounts[0].DeveloperID != "999" {
		t.Errorf("DeveloperID = %q, want 999 after round-trip", again.Accounts[0].DeveloperID)
	}
}
