package orchestrator_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

func TestRollout_malformedTrackIn2xx_exit30(t *testing.T) {
	cases := map[string]string{
		"releases under another key": `{"track":"production","Releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.01}]}`,
		"releases under two keys":    `{"track":"production","releases":[],"Releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.01}]}`,
		"null release":               `{"track":"production","releases":[null]}`,
	}
	for name, track := range cases {
		t.Run(name, func(t *testing.T) {
			rt := newPromoteFake(stateAPI{editID: "edit-rollout", sourceTrackGetResp: track})
			_, err := orchestrator.Rollout(context.Background(), &http.Client{Transport: rt}, orchestrator.StateOpts{
				Package:      "com.example.app",
				Track:        "production",
				UserFraction: 0.05,
				Confirm:      true,
			})
			if err == nil {
				t.Fatal("want error with exit 30, got nil")
			}
			var c interface{ ExitCode() int }
			if !errors.As(err, &c) || c.ExitCode() != 30 {
				t.Errorf("err = %v, want exit 30", err)
			}
			if !strings.Contains(err.Error(), "tracks.get on com.example.app: ") {
				t.Errorf("err = %q, want it to name tracks.get on the package", err)
			}
		})
	}
}
