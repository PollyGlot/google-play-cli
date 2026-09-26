package status_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/auth/status"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_inactive_golden freezes the no-account snapshot: only
// "active": false survives, every other key is omitted.
func TestRenderJSON_inactive_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "inactive.json.golden", status.Payload{Active: false})
}

// TestRenderJSON_keyring_golden freezes a stored Account in the OS keyring:
// no path key, since the credential has no file on disk.
func TestRenderJSON_keyring_golden(t *testing.T) {
	p := status.Payload{
		Active:      true,
		Name:        "ci",
		ClientEmail: "gplay-ci@example-project.iam.gserviceaccount.com",
		Backend:     "keyring",
	}
	outputtest.GoldenJSON(t, "keyring.json.golden", p)
}

// TestRenderJSON_fileBackend_golden freezes the file backend, the only
// branch that reports where the credential lives.
func TestRenderJSON_fileBackend_golden(t *testing.T) {
	p := status.Payload{
		Active:      true,
		Name:        "ci",
		ClientEmail: "gplay-ci@example-project.iam.gserviceaccount.com",
		Backend:     "file",
		Path:        "/home/dev/.config/gplay/accounts/ci.json",
	}
	outputtest.GoldenJSON(t, "file_backend.json.golden", p)
}

// TestRenderJSON_envOverride_golden freezes an inline credential: the
// placeholder name and no backend, because no keystore was probed.
func TestRenderJSON_envOverride_golden(t *testing.T) {
	p := status.Payload{
		Active:      true,
		Name:        "(env override)",
		ClientEmail: "gplay-ci@example-project.iam.gserviceaccount.com",
	}
	outputtest.GoldenJSON(t, "env_override.json.golden", p)
}
