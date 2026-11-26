package anomaliescmd

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestRun_undecodable2xxBody_exit30(t *testing.T) {
	rc, _, _ := newRC(t, `{"anomalies":[{"metricSet":5}]}`)
	_, err := Run(rc, Input{Package: "com.example.app", Since: "7d"})
	if got := exit.For(err); got != 30 {
		t.Errorf("exit.For(err) = %d, want 30; err=%v", got, err)
	}
}
