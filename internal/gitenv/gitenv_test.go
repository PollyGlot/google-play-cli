package gitenv_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/gitenv"
)

func TestSafe_dropsRedirectionAndGplayVariables(t *testing.T) {
	got := gitenv.Safe([]string{
		"PATH=/usr/bin",
		"GIT_DIR=/somewhere/.git",
		"GIT_WORK_TREE=/somewhere",
		"GIT_INDEX_FILE=/somewhere/.git/index",
		"GIT_OBJECT_DIRECTORY=/somewhere/.git/objects",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/tmp/hooks",
		`GPLAY_SERVICE_ACCOUNT={"type":"service_account","private_key":"-----BEGIN PRIVATE KEY-----"}`,
		"GPLAY_ACCOUNT=prod",
		"GPLAY_DEVELOPER_ID=1234567890",
		"GIT_SSL_CAINFO=/etc/ssl/corp.pem",
		"HTTPS_PROXY=http://proxy.test:3128",
		"HOME=/home/u",
	})
	want := []string{"PATH=/usr/bin", "GIT_SSL_CAINFO=/etc/ssl/corp.pem", "HTTPS_PROXY=http://proxy.test:3128", "HOME=/home/u"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Safe = %v, want %v", got, want)
	}
}
