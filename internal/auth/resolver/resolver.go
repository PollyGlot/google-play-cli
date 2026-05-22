// Package resolver picks the credential the next API call will use. It
// implements the credential resolution precedence documented in
// docs/DESIGN.md §1 — first match wins, in this order:
//
//  1. --service-account flag (path or inline JSON)
//  2. --account flag (stored Account name)
//  3. GPLAY_SERVICE_ACCOUNT env var (path or inline JSON)
//  4. GPLAY_ACCOUNT env var (stored Account name)
//  5. The active Account in config (after cascade: project-local override
//     beats global active flag — see config.Resolved.ConfigAccount)
//
// Inputs is the per-call carrier for flag values; Deps carries the
// resolved config cascade and the keystore backend. Both are populated
// by the caller (typically the kernel) so Resolve stays a pure function
// with no hidden globals or singletons.
package resolver

import (
	"context"
	"unicode"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

// Env-var names read by Resolve. Exposed as constants so tests and
// documentation reference them by symbol rather than string literals.
const (
	EnvServiceAccount = "GPLAY_SERVICE_ACCOUNT"
	EnvAccount        = "GPLAY_ACCOUNT"
)

// noSourceError is the type behind ErrNoSource. Carrying ExitCode() on the
// value lets exit.For dispatch without a sentinel-specific branch in
// cmd/gplay/main.go. The empty struct stays comparable so existing
// `errors.Is(err, ErrNoSource)` call sites keep working.
type noSourceError struct{}

func (noSourceError) Error() string {
	return "resolver: no credential source; pass --service-account, set GPLAY_SERVICE_ACCOUNT, " +
		"or run `gplay auth login`"
}

func (noSourceError) ExitCode() int { return 10 }

// ErrNoSource is returned when no precedence layer yields a credential.
// The command layer maps this to exit code 10 with a hint pointing at
// `gplay auth login` and the env-var docs.
var ErrNoSource error = noSourceError{}

// Deps carries the resolved cascade and the keystore backend. Build it
// once per invocation in the kernel and hand the same value to every
// Resolve call.
type Deps struct {
	Resolved *config.Resolved
	Keystore keystore.Backend
}

// Inputs carries the per-call flag values fed to Resolve. A zero-value
// Inputs falls straight through to layer 5 (Deps.Resolved.ConfigAccount).
//
// EnvServiceAccount / EnvAccount are read once by the caller (typically
// the kernel via os.Getenv) and passed in — the resolver stays a pure
// function with no hidden process state.
type Inputs struct {
	// ServiceAccountFlag is the value of `--service-account` (path or inline
	// JSON). Empty means the flag was not set.
	ServiceAccountFlag string
	// AccountFlag is the value of `--account` (stored Account name). Empty
	// means the flag was not set.
	AccountFlag string
	// EnvServiceAccount is the value of $GPLAY_SERVICE_ACCOUNT. The
	// caller reads it once at boot and passes it in.
	EnvServiceAccount string
	// EnvAccount is the value of $GPLAY_ACCOUNT, same shape as above.
	EnvAccount string
}

// Resolve walks the precedence chain in order and returns the first
// service account that resolves, or ErrNoSource if none does.
//
// ctx is threaded through for future cancellation support (env var lookup
// and JSON parsing themselves don't block, but a keystore Load on a slow
// backend may). The function itself is otherwise pure: no globals, no
// singletons.
func Resolve(_ context.Context, deps Deps, in Inputs) (*serviceaccount.ServiceAccount, error) {
	// Layer 1: --service-account flag (inline JSON or path).
	if in.ServiceAccountFlag != "" {
		return loadServiceAccount(in.ServiceAccountFlag)
	}

	// Layer 2: --account flag (stored Account name).
	if in.AccountFlag != "" {
		return loadStoredAccount(deps.Keystore, in.AccountFlag)
	}

	// Layer 3: GPLAY_SERVICE_ACCOUNT env var (read by caller, passed
	// in via Inputs.EnvServiceAccount).
	if in.EnvServiceAccount != "" {
		return loadServiceAccount(in.EnvServiceAccount)
	}

	// Layer 4: GPLAY_ACCOUNT env var.
	if in.EnvAccount != "" {
		return loadStoredAccount(deps.Keystore, in.EnvAccount)
	}

	// Layer 5: ConfigAccount from cascade (project-local override > global active).
	if deps.Resolved == nil || deps.Resolved.ConfigAccount == "" {
		return nil, ErrNoSource
	}
	return loadStoredAccount(deps.Keystore, deps.Resolved.ConfigAccount)
}

// loadStoredAccount loads a credential from the keystore by name and
// parses it as a service account.
func loadStoredAccount(ks keystore.Backend, name string) (*serviceaccount.ServiceAccount, error) {
	data, err := ks.Load(name)
	if err != nil {
		return nil, err
	}
	return serviceaccount.Parse(data)
}

// loadServiceAccount accepts either an inline JSON string (first
// non-whitespace byte is `{`) or a filesystem path, and returns the
// parsed service account. The detection rule is documented in
// docs/DESIGN.md §1 and applies to both --service-account and
// GPLAY_SERVICE_ACCOUNT.
func loadServiceAccount(value string) (*serviceaccount.ServiceAccount, error) {
	if isInlineJSON(value) {
		return serviceaccount.Parse([]byte(value))
	}
	return serviceaccount.Load(value)
}

// isInlineJSON reports whether value should be treated as inline JSON
// rather than a filesystem path. The rule (per docs/DESIGN.md §1):
// strip leading whitespace; if the first byte is `{`, treat as JSON.
func isInlineJSON(value string) bool {
	for _, r := range value {
		if unicode.IsSpace(r) {
			continue
		}
		return r == '{'
	}
	return false
}
