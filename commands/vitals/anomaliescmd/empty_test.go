package anomaliescmd

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestRun_empty2xxBody_isAnEmptyResult(t *testing.T) {
	rc, _, _ := newRC(t, ``)
	r, err := Run(rc, Input{Package: "com.example.app"})
	if got := exit.For(err); got != 0 {
		t.Fatalf("exit.For(err) = %d, want 0; err=%v", got, err)
	}
	p, ok := r.(Payload)
	if !ok {
		t.Fatalf("Run returned %T, want Payload", r)
	}
	if len(p.Anomalies) != 0 {
		t.Errorf("anomalies = %+v, want none", p.Anomalies)
	}
	if string(p.Raw) != `{"anomalies":[]}` {
		t.Errorf("raw = %s, want the empty envelope", p.Raw)
	}
}
