package status_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/auth/status"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_inactive_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "inactive.json.golden", status.Payload{Active: false})
}

func TestRenderJSON_keyring_golden(t *testing.T) {
	p := status.Payload{
		Active:      true,
		Name:        "ci",
		ClientEmail: "gplay-ci@example-project.iam.gserviceaccount.com",
		Backend:     "keyring",
	}
	outputtest.GoldenJSON(t, "keyring.json.golden", p)
}

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

func TestRenderJSON_envOverride_golden(t *testing.T) {
	p := status.Payload{
		Active:      true,
		Name:        "(env override)",
		ClientEmail: "gplay-ci@example-project.iam.gserviceaccount.com",
	}
	outputtest.GoldenJSON(t, "env_override.json.golden", p)
}
