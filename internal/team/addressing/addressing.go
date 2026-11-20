package addressing

import (
	"os"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

const EnvDeveloperID = "GPLAY_DEVELOPER_ID"

type unresolvedError struct{}

func (unresolvedError) Error() string {
	return "no developer-id resolved for the active Account: set it with `gplay auth login --developer-id <id>`, " +
		"export GPLAY_DEVELOPER_ID, pin it in .gplay/config.local.json, or pass --developer-id <id> " +
		"(find it in the Play Console URL: play.google.com/console/u/0/developers/<developerId>/...)"
}
func (unresolvedError) ExitCode() int { return 10 }

var ErrUnresolved error = unresolvedError{}

func ForRun(rc *kernel.RunContext, flag string) (string, error) {
	id, err := Resolve(flag, os.Getenv(EnvDeveloperID), rc.Resolved)
	if err == nil {
		return id, nil
	}
	if aerr := rc.RequireAccount(); aerr != nil {
		return "", aerr
	}
	return "", err
}

func Resolve(flag, env string, resolved *config.Resolved) (string, error) {
	if v := strings.TrimSpace(flag); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(env); v != "" {
		return v, nil
	}
	if resolved != nil {
		if v := strings.TrimSpace(resolved.DeveloperID); v != "" {
			return v, nil
		}
	}
	return "", ErrUnresolved
}
