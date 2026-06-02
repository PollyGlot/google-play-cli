package exit_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// customCoder is a local stand-in proving that exit.For delegates to any
// error that implements Coder — not just the ones shipped with gplay.
type customCoder struct{ code int }

func (c *customCoder) Error() string { return "custom" }
func (c *customCoder) ExitCode() int { return c.code }

func TestFor_mapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"missing-field", &serviceaccount.MissingFieldError{Field: "client_email"}, 10},
		{"wrapped-missing-field", fmt.Errorf("login: %w", &serviceaccount.MissingFieldError{Field: "private_key"}), 10},
		{"oauth-401", &token.AuthError{StatusCode: 401, Body: "denied"}, 10},
		{"wrapped-oauth", fmt.Errorf("doctor: %w", &token.AuthError{StatusCode: 403, Body: "forbidden"}), 10},
		{"no-source", resolver.ErrNoSource, 10},
		{"wrapped-no-source", fmt.Errorf("status: %w", resolver.ErrNoSource), 10},
		{"unknown-account", config.ErrUnknownAccount, 2},
		{"wrapped-unknown-account", fmt.Errorf("logout: %w", config.ErrUnknownAccount), 2},
		{"usage-error", &exit.UsageError{Msg: "bad flag"}, 2},
		{"wrapped-usage-error", fmt.Errorf("list: %w", exit.Usagef("unknown column %q", "bogus")), 2},
		{"custom-coder", &customCoder{code: 42}, 42},
		{"generic", errors.New("boom"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exit.For(tc.err); got != tc.want {
				t.Errorf("exit.For(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
