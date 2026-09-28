package gitenv_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/gitenv"
)

func TestSafe_dropsRepositoryRedirection(t *testing.T) {
	got := gitenv.Safe([]string{
		"PATH=/usr/bin",
		"GIT_DIR=/somewhere/.git",
		"GIT_WORK_TREE=/somewhere",
		"GIT_INDEX_FILE=/somewhere/.git/index",
		"GIT_OBJECT_DIRECTORY=/somewhere/.git/objects",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/tmp/hooks",
		// Kept: this is how a locked-down network reaches the remote at all.
		"GIT_SSL_CAINFO=/etc/ssl/corp.pem",
		"HTTPS_PROXY=http://proxy.test:3128",
	})
	want := []string{"PATH=/usr/bin", "GIT_SSL_CAINFO=/etc/ssl/corp.pem", "HTTPS_PROXY=http://proxy.test:3128"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Safe = %v, want %v", got, want)
	}
}
