package login_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestLogin_base64Value_isRefusedWithoutEcho(t *testing.T) {
	value := base64.StdEncoding.EncodeToString([]byte(validSAJSON))
	stdout, stderr, boot := newCmd(t)

	err := runCmd(t, boot, stdout, stderr, "--service-account", value)
	if err == nil {
		t.Fatal("Execute: expected an error for a base64 value, got nil")
	}
	if code := exit.For(err); code != 10 {
		t.Errorf("exit code = %d, want 10", code)
	}
	all := err.Error() + stdout.String() + stderr.String()
	if strings.Contains(all, value[:24]) {
		t.Errorf("the value was echoed back:\n%s", all)
	}
	if !strings.Contains(err.Error(), "base64-encoded") {
		t.Errorf("error %q does not name the base64 shape", err)
	}
}

func TestLogin_inlineJSON_registersAccount(t *testing.T) {
	stdout, stderr, boot := newCmd(t)

	if err := runCmd(t, boot, stdout, stderr, "--service-account", validSAJSON); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if a, ok := cfg.Active(); !ok || a.Name != "playci" {
		t.Errorf("active account = %+v (ok=%v), want playci", a, ok)
	}
}
