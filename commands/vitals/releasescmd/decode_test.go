package releasescmd

import (
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestRun_undecodable2xxBody_exit30(t *testing.T) {
	rc, _ := newRC(t, http.StatusOK, `{"tracks":"x"}`)
	_, err := Run(rc, Input{Package: "com.example.app"})
	if got := exit.For(err); got != 30 {
		t.Errorf("exit.For(err) = %d, want 30; err=%v", got, err)
	}
}
