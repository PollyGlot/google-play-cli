package doctor_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

func TestChecks_everyCheckHasARun(t *testing.T) {
	_, obs := transport.WithScopeObserver(nil)
	checks := append(doctor.DefaultChecks(obs), doctor.CheckPackageAccess("com.example.app"))
	for _, c := range checks {
		if c.Run == nil {
			t.Errorf("check %q has a nil Run", c.Name)
		}
	}
}
