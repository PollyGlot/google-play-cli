package teamcmd

import (
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/team/addressing"
)

func DeveloperID(rc *kernel.RunContext, flag string) (string, error) {
	id, err := addressing.ForRun(rc, flag)
	if err != nil {
		return "", err
	}
	if f := strings.TrimSpace(flag); f != "" {
		captureTypeOnce(rc, f)
	}
	return id, nil
}

func captureTypeOnce(rc *kernel.RunContext, id string) {
	if rc.AccountName == "" || rc.ConfigPath == "" {
		return
	}
	g, err := config.LoadGlobalOrEmpty(rc.Ctx, fsOrDefault(rc), rc.ConfigPath)
	if err != nil {
		return
	}
	for i := range g.Accounts {
		if g.Accounts[i].Name != rc.AccountName {
			continue
		}
		if strings.TrimSpace(g.Accounts[i].DeveloperID) != "" {
			return
		}
		g.Accounts[i].DeveloperID = id
		if err := g.Save(rc.Ctx, fsOrDefault(rc), rc.ConfigPath); err != nil {
			rc.Warnf("could not persist developer-id to Account %q: %v", rc.AccountName, err)
			return
		}
		rc.Confirmf("developer-id %s saved to Account %q", id, rc.AccountName)
		return
	}
}

func fsOrDefault(rc *kernel.RunContext) config.FS {
	if rc.FS != nil {
		return rc.FS
	}
	return config.OSFS{}
}
