// Package config owns gplay's cascading configuration model.
//
// The merge order is:
//
//	$XDG_CONFIG_HOME/gplay/config.json     (global, machine-local)
//	<repo>/.gplay/config.json              (project shared, committed)
//	<repo>/.gplay/config.local.json        (project local, gitignored)
//	GPLAY_* env vars                       (handled by callers; per docs/DESIGN.md §1)
//	CLI flags                              (handled by callers; per docs/DESIGN.md §1)
//
// Later wins. See ADR 0004 for the rationale.
//
// The package exposes two views:
//
//  1. Global — on-disk shape of the global layer alone. Used by `auth login`
//     / `auth list` / `auth logout` (which only ever touch accounts).
//  2. Resolved — the merged view used by every command at runtime.
//
// Account name selection (`Resolved.ConfigAccount`) is resolved here only
// for the in-config layers (project-local override → global active).
// Env- and flag-based overrides are interleaved with the SA-bytes layers
// inside `internal/auth/resolver`; surfacing them here too would force the
// resolver to second-guess us, so we don't.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PollyGlot/google-play-cli/internal/walkup"
)

// Account is the human-friendly registration of a service-account credential.
// The credential bytes themselves live in the keystore, keyed by Name.
type Account struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// Global is the on-disk shape of $XDG_CONFIG_HOME/gplay/config.json.
type Global struct {
	Accounts []Account `json:"accounts"`
}

// ErrUnknownAccount is returned when an operation references an account name
// that is not in the registry.
var ErrUnknownAccount = errors.New("config: unknown account")

// LoadGlobalOrEmpty reads the global layer. If the file does not exist, an
// empty Global is returned without error (lazy creation by `auth login`).
func LoadGlobalOrEmpty(path string) (*Global, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Global{}, nil
	}
	if err != nil {
		return nil, err
	}
	var g Global
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &g, nil
}

// Save writes the global config to path with mode 0600, creating the parent
// directory if needed.
func (g *Global) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// AddAccount upserts an account by name. Calling AddAccount with a name that
// already exists is a no-op (preserves Active flag and ordering).
func (g *Global) AddAccount(name string) {
	for _, a := range g.Accounts {
		if a.Name == name {
			return
		}
	}
	g.Accounts = append(g.Accounts, Account{Name: name})
}

// RemoveAccount deletes an account by name. If the removed account was the
// active one, the registry is left with no active account — callers decide
// whether to choose a new active explicitly. Returns ErrUnknownAccount if
// name is not in the registry.
func (g *Global) RemoveAccount(name string) error {
	for i, a := range g.Accounts {
		if a.Name == name {
			g.Accounts = append(g.Accounts[:i], g.Accounts[i+1:]...)
			return nil
		}
	}
	return ErrUnknownAccount
}

// SetActive marks one account as Active and clears the flag on every other.
// Returns ErrUnknownAccount if name is not in the registry.
func (g *Global) SetActive(name string) error {
	found := false
	for i := range g.Accounts {
		if g.Accounts[i].Name == name {
			g.Accounts[i].Active = true
			found = true
		} else {
			g.Accounts[i].Active = false
		}
	}
	if !found {
		return ErrUnknownAccount
	}
	return nil
}

// Active returns the currently active account, or false if none is set.
func (g *Global) Active() (Account, bool) {
	for _, a := range g.Accounts {
		if a.Active {
			return a, true
		}
	}
	return Account{}, false
}

// LoadOptions controls Load.
type LoadOptions struct {
	// GlobalPath is the absolute path of $XDG_CONFIG_HOME/gplay/config.json.
	// Required.
	GlobalPath string
	// StartDir is the directory the walk-up starts from (typically cwd).
	// Required.
	StartDir string
	// HomeDir is the user's home directory. Walk-up refuses to consider
	// .gplay/ at or above HomeDir, preventing a rogue ~/.gplay/config.json
	// from masquerading as a project pin. Required.
	HomeDir string
}

// Resolved is the merged view of all configuration sources. Field semantics:
//
//   - Pin: the package pinned by .gplay/config.json (project shared layer),
//     empty if no .gplay/config.json was found via walk-up.
//   - ConfigAccount: the account name resolved from the in-config layers:
//     .gplay/config.local.json's `account` field overrides the global layer's
//     active flag. Empty if neither is set.
//   - Accounts: the accounts list from the global layer. Used by `auth list`
//     and friends to enumerate registered Accounts.
//   - GlobalPath: always populated (echoes LoadOptions.GlobalPath).
//   - ProjectSharedPath: path of the .gplay/config.json found via walk-up,
//     or "" if none was found.
//   - ProjectLocalPath: path of the .gplay/config.local.json read from the
//     same .gplay/ as ProjectSharedPath, or "" if no .gplay/ dir was found.
type Resolved struct {
	Pin           string
	ConfigAccount string
	Accounts      []Account

	GlobalPath        string
	ProjectSharedPath string
	ProjectLocalPath  string
}

// projectShared mirrors the on-disk shape of <repo>/.gplay/config.json.
// The `account` field is included so we can detect (and reject) it being
// present in a committed config — see ADR 0004.
type projectShared struct {
	Package string `json:"package"`
	Account string `json:"account"`
}

// projectLocal mirrors the on-disk shape of <repo>/.gplay/config.local.json.
type projectLocal struct {
	Account string `json:"account"`
	Package string `json:"package"`
}

// Load runs the cascade. It reads each layer that exists, validates that
// the committed layer does not pin an `account`, and returns the merged
// view in *Resolved.
func Load(opts LoadOptions) (*Resolved, error) {
	r := &Resolved{GlobalPath: opts.GlobalPath}

	// Global layer — accounts list + active flag.
	g, err := LoadGlobalOrEmpty(opts.GlobalPath)
	if err != nil {
		return nil, err
	}
	r.Accounts = g.Accounts
	if a, ok := g.Active(); ok {
		r.ConfigAccount = a.Name
	}

	// Walk up to find the nearest .gplay/ directory. Refuse to descend
	// into $HOME so a stray ~/.gplay can't hijack repos.
	repoRoot, err := walkup.FindFileExcluding(opts.StartDir, ".gplay", opts.HomeDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		gplayDir := filepath.Join(repoRoot, ".gplay")
		// Project shared (committed) — only `package` allowed.
		sharedPath := filepath.Join(gplayDir, "config.json")
		if exists(sharedPath) {
			r.ProjectSharedPath = sharedPath
			ps, err := readProjectShared(sharedPath)
			if err != nil {
				return nil, err
			}
			r.Pin = ps.Package
		}
		// Project local (gitignored) — may override account and (rarely) package.
		localPath := filepath.Join(gplayDir, "config.local.json")
		if exists(localPath) {
			r.ProjectLocalPath = localPath
			pl, err := readProjectLocal(localPath)
			if err != nil {
				return nil, err
			}
			if pl.Account != "" {
				r.ConfigAccount = pl.Account
			}
			if pl.Package != "" {
				r.Pin = pl.Package
			}
		}
	}

	return r, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readProjectShared(path string) (*projectShared, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if _, has := raw["account"]; has {
		return nil, fmt.Errorf("config: %s: field %q is forbidden in committed config (account names are machine-local; put it in .gplay/config.local.json or set GPLAY_ACCOUNT instead)", path, "account")
	}
	var ps projectShared
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &ps, nil
}

func readProjectLocal(path string) (*projectLocal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pl projectLocal
	if err := json.Unmarshal(data, &pl); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &pl, nil
}
