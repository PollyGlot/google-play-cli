package addressing_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/team/addressing"
)

// TestResolve_cascade asserts the ADR-0015 precedence (later wins): flag beats
// env beats the config-merged Resolved.DeveloperID.
func TestResolve_cascade(t *testing.T) {
	for _, tc := range []struct {
		name     string
		flag     string
		env      string
		resolved string
		want     string
	}{
		{"flag wins over all", "F", "E", "C", "F"},
		{"env wins over config", "", "E", "C", "E"},
		{"config when no flag/env", "", "", "C", "C"},
		{"flag trims whitespace then config", "  ", "", "C", "C"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := addressing.Resolve(tc.flag, tc.env, &config.Resolved{DeveloperID: tc.resolved})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolve_unresolved_exit10 asserts that no resolvable id is an
// auth-family failure (exit 10) whose message names how to set one.
func TestResolve_unresolved_exit10(t *testing.T) {
	_, err := addressing.Resolve("", "", &config.Resolved{})
	if err == nil {
		t.Fatal("expected an unresolved error")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 10 {
		t.Fatalf("err = %v, want exit 10", err)
	}
	for _, want := range []string{"auth login --developer-id", "GPLAY_DEVELOPER_ID", "--developer-id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err.Error(), want)
		}
	}
}

// TestResolve_nilResolved asserts a nil Resolved degrades to unresolved rather
// than panicking.
func TestResolve_nilResolved(t *testing.T) {
	if _, err := addressing.Resolve("", "", nil); !errors.Is(err, addressing.ErrUnresolved) {
		t.Errorf("nil Resolved: err = %v, want ErrUnresolved", err)
	}
	if got, err := addressing.Resolve("X", "", nil); err != nil || got != "X" {
		t.Errorf("flag with nil Resolved: got %q, err %v", got, err)
	}
}
