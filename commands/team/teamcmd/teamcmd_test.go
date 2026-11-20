package teamcmd_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/team/teamcmd"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

func rcWithAccount(t *testing.T, accountName, existingDevID string) (*kernel.RunContext, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	g := &config.Global{Accounts: []config.Account{{Name: accountName, Active: true, DeveloperID: existingDevID}}}
	if err := g.Save(context.Background(), config.OSFS{}, cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rc := kernel.NewForTest(context.Background(), kernel.Boot{ConfigPath: cfgPath}, kernel.Inputs{})
	rc.AccountName = accountName
	rc.ConfigPath = cfgPath
	rc.FS = config.OSFS{}
	rc.Stderr = &bytes.Buffer{}
	rc.Resolved = &config.Resolved{ConfigAccount: accountName, DeveloperID: existingDevID}
	return rc, cfgPath
}

func devIDOnDisk(t *testing.T, cfgPath, accountName string) string {
	t.Helper()
	g, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	for _, a := range g.Accounts {
		if a.Name == accountName {
			return a.DeveloperID
		}
	}
	t.Fatalf("account %q vanished from config", accountName)
	return ""
}

func TestDeveloperID_typeOnceCapture(t *testing.T) {
	t.Setenv("GPLAY_DEVELOPER_ID", "")
	rc, cfgPath := rcWithAccount(t, "ci", "")

	got, err := teamcmd.DeveloperID(rc, "555")
	if err != nil {
		t.Fatalf("DeveloperID: %v", err)
	}
	if got != "555" {
		t.Errorf("resolved = %q, want 555", got)
	}
	if onDisk := devIDOnDisk(t, cfgPath, "ci"); onDisk != "555" {
		t.Errorf("developer-id on disk = %q, want 555 (type-once capture)", onDisk)
	}

	rc2, _ := rcWithAccount(t, "ci", "555")
	got2, err := teamcmd.DeveloperID(rc2, "")
	if err != nil {
		t.Fatalf("second DeveloperID: %v", err)
	}
	if got2 != "555" {
		t.Errorf("second resolve = %q, want the persisted 555", got2)
	}
}

func TestDeveloperID_explicitFlag_doesNotOverwrite(t *testing.T) {
	t.Setenv("GPLAY_DEVELOPER_ID", "")
	rc, cfgPath := rcWithAccount(t, "ci", "orig")

	got, err := teamcmd.DeveloperID(rc, "override")
	if err != nil {
		t.Fatalf("DeveloperID: %v", err)
	}
	if got != "override" {
		t.Errorf("resolved = %q, want the flag override", got)
	}
	if onDisk := devIDOnDisk(t, cfgPath, "ci"); onDisk != "orig" {
		t.Errorf("developer-id on disk = %q, want orig (no silent re-persist)", onDisk)
	}
}

func TestDeveloperID_adHocCredential_noPersist(t *testing.T) {
	t.Setenv("GPLAY_DEVELOPER_ID", "")
	rc := kernel.NewForTest(context.Background(), kernel.Boot{}, kernel.Inputs{})
	rc.AccountName = ""
	rc.Resolved = &config.Resolved{}
	rc.Stderr = &bytes.Buffer{}

	got, err := teamcmd.DeveloperID(rc, "777")
	if err != nil {
		t.Fatalf("DeveloperID: %v", err)
	}
	if got != "777" {
		t.Errorf("resolved = %q, want 777", got)
	}
}

func TestDeveloperID_unresolved_exit10(t *testing.T) {
	t.Setenv("GPLAY_DEVELOPER_ID", "")
	rc := kernel.NewForTest(context.Background(), kernel.Boot{}, kernel.Inputs{})
	rc.Resolved = &config.Resolved{}

	_, err := teamcmd.DeveloperID(rc, "")
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 10 {
		t.Fatalf("err = %v, want exit 10", err)
	}
}
