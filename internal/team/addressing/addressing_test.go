package addressing_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/team/addressing"
)

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

func TestResolve_nilResolved(t *testing.T) {
	if _, err := addressing.Resolve("", "", nil); !errors.Is(err, addressing.ErrUnresolved) {
		t.Errorf("nil Resolved: err = %v, want ErrUnresolved", err)
	}
	if got, err := addressing.Resolve("X", "", nil); err != nil || got != "X" {
		t.Errorf("flag with nil Resolved: got %q, err %v", got, err)
	}
}

func TestForRun_noAccount_namesTheAccountNotTheDeveloperID(t *testing.T) {
	t.Setenv(addressing.EnvDeveloperID, "")
	rc := kernel.NewForTest(context.Background(), kernel.Boot{}, kernel.Inputs{})
	rc.Resolved = &config.Resolved{}

	_, err := addressing.ForRun(rc, "")
	if err == nil {
		t.Fatal("expected a no-Account error")
	}
	if code := exit.For(err); code != 10 {
		t.Errorf("exit.For = %d, want 10", code)
	}
	if err.Error() != kernel.NoAccountError().Error() {
		t.Errorf("err = %q, want the one no-Account wording %q", err, kernel.NoAccountError())
	}
}

func TestForRun_accountWithoutDeveloperID_keepsTheDeveloperIDError(t *testing.T) {
	t.Setenv(addressing.EnvDeveloperID, "")
	rc := kernel.NewForTest(context.Background(), kernel.Boot{}, kernel.Inputs{})
	rc.Resolved = &config.Resolved{}
	rc.Account = &serviceaccount.ServiceAccount{ClientEmail: "ci@example.iam.gserviceaccount.com"}

	if _, err := addressing.ForRun(rc, ""); !errors.Is(err, addressing.ErrUnresolved) {
		t.Errorf("err = %v, want ErrUnresolved", err)
	}
	if got, err := addressing.ForRun(rc, "123"); err != nil || got != "123" {
		t.Errorf("flag: got %q, err %v, want 123", got, err)
	}
}
