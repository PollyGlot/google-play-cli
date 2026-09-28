package kernel_test

import (
	"errors"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestPackage pins the one precedence every package-axis command shares:
// trimmed --package, else the trimmed Project pin, else exit 2 with the
// canonical message. The nil cases are the bare contexts tests build.
func TestPackage(t *testing.T) {
	withPin := func(pin string) *kernel.RunContext {
		return &kernel.RunContext{Resolved: &config.Resolved{Pin: pin}}
	}
	cases := []struct {
		name string
		rc   *kernel.RunContext
		flag string
		want string
	}{
		{"flag wins over pin", withPin("com.pin"), "com.flag", "com.flag"},
		{"flag is trimmed", withPin("com.pin"), "  com.flag\t", "com.flag"},
		{"blank flag falls back to pin", withPin("com.pin"), "   ", "com.pin"},
		{"pin is trimmed", withPin(" com.pin "), "", "com.pin"},
		{"nil receiver takes the flag", nil, "com.flag", "com.flag"},
		{"nil cascade takes the flag", &kernel.RunContext{}, "com.flag", "com.flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.rc.Package(tc.flag)
			if err != nil {
				t.Fatalf("Package(%q) error = %v", tc.flag, err)
			}
			if got != tc.want {
				t.Errorf("Package(%q) = %q, want %q", tc.flag, got, tc.want)
			}
		})
	}
}

func TestPackage_noneIsUsageError(t *testing.T) {
	for name, rc := range map[string]*kernel.RunContext{
		"nil receiver": nil,
		"nil cascade":  {},
		"blank pin":    {Resolved: &config.Resolved{Pin: "  "}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := rc.Package(" ")
			var ue *exit.UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("error = %T %v, want *exit.UsageError", err, err)
			}
			if ue.Msg != kernel.NoPackageMsg {
				t.Errorf("message = %q, want %q", ue.Msg, kernel.NoPackageMsg)
			}
			if got := exit.For(err); got != 2 {
				t.Errorf("exit.For = %d, want 2", got)
			}
		})
	}
}
