package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
)

func TestExitCode_mapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"missing-field", &serviceaccount.MissingFieldError{Field: "client_email"}, 10},
		{"wrapped-missing-field", fmt.Errorf("login: %w", &serviceaccount.MissingFieldError{Field: "private_key"}), 10},
		{"oauth-401", &token.AuthError{StatusCode: 401, Body: "denied"}, 10},
		{"no-source", resolver.ErrNoSource, 10},
		{"wrapped-no-source", fmt.Errorf("status: %w", resolver.ErrNoSource), 10},
		{"generic", errors.New("boom"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestRootCmd_persistentFlags_serviceAccountAndAccount(t *testing.T) {
	root := newRootCmd(rootOptions{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	for _, name := range []string{"service-account", "account"} {
		f := root.PersistentFlags().Lookup(name)
		if f == nil {
			t.Errorf("root command missing persistent --%s flag", name)
		}
	}
}
