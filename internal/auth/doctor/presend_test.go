package doctor_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/doctor"
)

func TestCheckPackageAccess_unbuildableInsert_exit1_notNetwork(t *testing.T) {
	rt := &packageRT{t: t}
	results := doctor.Run(ctxWithRT(t, rt.serve), makeSignedSA(t), nil, doctor.CheckPackageAccess(""))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Passed || r.ExitCode != 1 {
		t.Errorf("result = %+v, want a failed check with exit 1", r)
	}
	if strings.Contains(r.Hint, "network") || strings.Contains(r.Hint, "retry") {
		t.Errorf("hint %q reports a request that was never built as a network failure", r.Hint)
	}
}
