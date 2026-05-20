// Package resolver picks the credential the next API call will use. It
// implements the credential resolution precedence documented in
// docs/DESIGN.md §1 — first match wins, in this order:
//
//  1. --service-account flag (path or inline JSON)
//  2. --account flag (stored Account name)
//  3. GPLAY_SERVICE_ACCOUNT env var (path or inline JSON)
//  4. GPLAY_ACCOUNT env var (stored Account name)
//  5. The active Account in config
//
// Inputs is the per-call carrier for flag and env values; the call site
// (typically the root command) is responsible for populating it from
// cobra flags and os.Getenv.
package resolver

import (
	"errors"
	"os"
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

// ErrNoSource is returned when no precedence layer yields a credential.
// The command layer maps this to exit code 10 with a hint pointing at
// `gplay auth login` and the env-var docs.
var ErrNoSource = errors.New(
	"resolver: no credential source; pass --service-account, set GPLAY_SERVICE_ACCOUNT, " +
		"or run `gplay auth login`")

// Inputs carries the per-call flag and env-var values fed to Resolve. A
// zero-value Inputs falls straight through to layer 5 (active Account).
type Inputs struct {
	// ServiceAccountFlag is the value of `--service-account` (path or inline
	// JSON). Empty means the flag was not set.
	ServiceAccountFlag string
	// AccountFlag is the value of `--account` (stored Account name). Empty
	// means the flag was not set.
	AccountFlag string
}

// Resolver glues a config (which Account is active, what's registered) and
// a keystore (where the SA JSON bytes live) into a single Resolve call.
type Resolver struct {
	cfg *config.Config
	ks  keystore.Backend
}

// New returns a Resolver backed by cfg and ks. Both are required.
func New(cfg *config.Config, ks keystore.Backend) *Resolver {
	return &Resolver{cfg: cfg, ks: ks}
}

// Resolve walks the precedence chain in order and returns the first
// service account that resolves, or ErrNoSource if none does.
func (r *Resolver) Resolve(in Inputs) (*serviceaccount.ServiceAccount, error) {
	// Layer 1: --service-account flag (inline JSON or path).
	if in.ServiceAccountFlag != "" {
		return loadServiceAccount(in.ServiceAccountFlag)
	}

	// Layer 2: --account flag (stored Account name).
	if in.AccountFlag != "" {
		return r.loadStoredAccount(in.AccountFlag)
	}

	// Layer 3: GPLAY_SERVICE_ACCOUNT env var (path or inline JSON).
	if v := os.Getenv(EnvServiceAccount); v != "" {
		return loadServiceAccount(v)
	}

	// Layer 4: GPLAY_ACCOUNT env var (stored Account name).
	if v := os.Getenv(EnvAccount); v != "" {
		return r.loadStoredAccount(v)
	}

	// Layer 5: active Account in config.
	a, ok := r.cfg.Active()
	if !ok {
		return nil, ErrNoSource
	}
	return r.loadStoredAccount(a.Name)
}

// loadStoredAccount loads a credential from the keystore by name and
// parses it as a service account.
func (r *Resolver) loadStoredAccount(name string) (*serviceaccount.ServiceAccount, error) {
	data, err := r.ks.Load(name)
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
