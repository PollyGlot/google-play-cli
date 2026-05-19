// Package resolver picks the credential the next API call will use. This
// foundation slice only implements the "active Account from config" path;
// the full precedence chain (flag → env → active) lands in a follow-up.
package resolver

import (
	"errors"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

// ErrNoActive is returned when no Account is marked active in the config.
// The command layer maps this to exit code 10 with a hint pointing at
// `gplay auth login`.
var ErrNoActive = errors.New("resolver: no active account; run `gplay auth login`")

// Resolver glues a config (which Account is active) and a keystore (where
// the SA JSON bytes live) into a single Resolve call.
type Resolver struct {
	cfg *config.Config
	ks  keystore.Backend
}

// New returns a Resolver backed by cfg and ks. Both are required.
func New(cfg *config.Config, ks keystore.Backend) *Resolver {
	return &Resolver{cfg: cfg, ks: ks}
}

// Resolve returns the parsed service account of the active Account.
func (r *Resolver) Resolve() (*serviceaccount.ServiceAccount, error) {
	a, ok := r.cfg.Active()
	if !ok {
		return nil, ErrNoActive
	}
	data, err := r.ks.Load(a.Name)
	if err != nil {
		return nil, err
	}
	return serviceaccount.Parse(data)
}
