// Package config owns gplay's cascading configuration model. See ADR-0004.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/PollyGlot/google-play-cli/internal/pathguard"
	"github.com/PollyGlot/google-play-cli/internal/walkup"
)

// Account is the human-friendly registration of a service-account credential.
// The credential bytes themselves live in the keystore, keyed by Name.
// Packages is the per-Account registry of Android package names this Account
// has been registered against via `gplay apps add`; the field is omitempty so
// pre-registry config files round-trip unchanged.
//
// DeveloperID is the Play Console Developer account this credential
// administers (ADR-0015): the org `gplay team` is keyed by. It rides on the
// Account (not the committed project pin) because the org follows the
// credential, not the repo, and the API offers no way to discover it; like
// Packages it is omitempty so pre-team config files round-trip unchanged.
type Account struct {
	Name        string   `json:"name"`
	Active      bool     `json:"active"`
	Packages    []string `json:"packages,omitempty"`
	DeveloperID string   `json:"developerId,omitempty"`
}

// Global is the on-disk shape of $XDG_CONFIG_HOME/gplay/config.json.
type Global struct {
	Accounts []Account `json:"accounts"`
}

// unknownAccountError is the type behind ErrUnknownAccount. Carrying
// ExitCode() on the value lets exit.For dispatch the "unknown account"
// case to exit code 2 (CLI misuse: the user named something we don't
// know about) without a sentinel-specific branch in cmd/gplay/main.go.
type unknownAccountError struct{}

func (unknownAccountError) Error() string { return "config: unknown account" }
func (unknownAccountError) ExitCode() int { return 2 }

// ErrUnknownAccount is returned when an operation references an account name
// that is not in the registry.
var ErrUnknownAccount error = unknownAccountError{}

// LoadGlobalOrEmpty reads the global layer through fsys. If the file does
// not exist, an empty Global is returned without error (lazy creation by
// `auth login`). The ctx is threaded for future cancellation support; the
// underlying FS operations are synchronous today.
func LoadGlobalOrEmpty(_ context.Context, fsys FS, path string) (*Global, error) {
	data, err := fsys.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
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
// directory if needed. ctx is threaded for future cancellation; fsys is the
// FS seam: pass OSFS{} in production.
//
// The write is atomic when the underlying FS supports it: the data is
// written to a sibling `<path>.tmp` first, then renamed onto `path`.
// On POSIX (and Windows for same-volume renames), rename is atomic, so
// a crash, SIGKILL, or disk-full mid-write leaves the prior config.json
// intact instead of a half-truncated file that breaks every subsequent
// gplay invocation with "unexpected end of input". A best-effort
// Remove of the .tmp file runs on the failure path so a hung
// invocation does not strand stale fragments.
//
// The staging path is guarded, not just joined: `<path>.tmp` is a name nothing
// owns, so it can be pre-placed as a symlink and a plain WriteFile would follow
// it, turning "save the account registry" into "overwrite that file", with the
// operator's rights and 0600 on a file they did not name. Same shape as the
// edit pin's `.tmp` (PRD #459 / slice #461).
func (g *Global) Save(_ context.Context, fsys FS, path string) error {
	dir := filepath.Dir(path)
	if err := fsys.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	// A root that will not resolve means the directory is not on the real
	// filesystem (the MemFS seam the config tests run on): there is nothing to
	// contain against, and the lexical path is what fsys will use anyway.
	if root, rootErr := pathguard.Root(dir); rootErr == nil {
		if tmp, err = pathguard.ContainWrite(root, tmp); err != nil {
			return err
		}
	}
	if err := fsys.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := fsys.Rename(tmp, path); err != nil {
		_ = fsys.Remove(tmp)
		return err
	}
	return nil
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
// active one, the registry is left with no active account: callers decide
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

// SetDeveloperID records id as the DeveloperID of the named account, returning
// false if name is not in the registry. The capture points are
// `gplay auth login --developer-id` and the `team` type-once persistence
// (ADR-0015); it always targets the global Account record, never the committed
// project config.
func (g *Global) SetDeveloperID(name, id string) bool {
	for i := range g.Accounts {
		if g.Accounts[i].Name == name {
			g.Accounts[i].DeveloperID = id
			return true
		}
	}
	return false
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

// LoadOptions controls Load. All three fields are required.
type LoadOptions struct {
	// GlobalPath is the absolute path of $XDG_CONFIG_HOME/gplay/config.json.
	GlobalPath string
	// StartDir is the directory the walk-up starts from (typically cwd).
	StartDir string
	// HomeDir bounds the walk-up: .gplay/ at or above HomeDir is ignored,
	// blocking a rogue ~/.gplay/config.json from posing as a project pin.
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

	// DeveloperID is the developer-id merged from the in-config layers
	// (ADR-0015): the active Account's DeveloperID, overridden by the
	// project-local config.local.json's `developerId`. Empty if neither is
	// set. The command layer applies the higher-precedence GPLAY_DEVELOPER_ID
	// env var and --developer-id flag on top (later wins).
	DeveloperID string

	GlobalPath        string
	ProjectSharedPath string
	ProjectLocalPath  string
}

// projectShared mirrors the on-disk shape of <repo>/.gplay/config.json.
// Account is json.RawMessage (not a pointer) so we can detect any presence
// of the field: including `"account": null`, which a *json.RawMessage
// would silently unmarshal to nil and let through. Committed configs must
// never carry the field at all (see ADR-0004).
type projectShared struct {
	Package     string          `json:"package"`
	Account     json.RawMessage `json:"account"`
	DeveloperID json.RawMessage `json:"developerId"`
}

// projectLocal mirrors the on-disk shape of <repo>/.gplay/config.local.json.
// DeveloperID is the gitignored, multi-tenant developer-account override
// (ADR-0015).
type projectLocal struct {
	Account     string `json:"account"`
	DeveloperID string `json:"developerId"`
}

// Load runs the cascade. It reads each layer that exists through fsys,
// validates that the committed layer does not pin an `account`, and
// returns the merged view in *Resolved.
func Load(ctx context.Context, fsys FS, opts LoadOptions) (*Resolved, error) {
	// HomeDir empty would silently disable the walk-up barrier. The
	// other two fields degrade safely (missing global is empty Global;
	// empty StartDir resolves to cwd via filepath.Abs).
	if opts.HomeDir == "" {
		return nil, fmt.Errorf("config: LoadOptions.HomeDir is required")
	}
	r := &Resolved{GlobalPath: opts.GlobalPath}

	// Global layer: accounts list + active flag.
	g, err := LoadGlobalOrEmpty(ctx, fsys, opts.GlobalPath)
	if err != nil {
		return nil, err
	}
	r.Accounts = g.Accounts
	if a, ok := g.Active(); ok {
		r.ConfigAccount = a.Name
		r.DeveloperID = a.DeveloperID
	}

	// Walk up to find the nearest .gplay/ directory. Refuse to descend
	// into $HOME so a stray ~/.gplay can't hijack repos.
	repoRoot, err := walkup.FindFileExcluding(opts.StartDir, ".gplay", opts.HomeDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		gplayDir := filepath.Join(repoRoot, ".gplay")
		sharedPath := filepath.Join(gplayDir, "config.json")
		ps, err := readProjectShared(fsys, sharedPath)
		if err != nil {
			return nil, err
		}
		if ps != nil {
			r.ProjectSharedPath = sharedPath
			r.Pin = ps.Package
		}
		localPath := filepath.Join(gplayDir, "config.local.json")
		pl, err := readProjectLocal(fsys, localPath)
		if err != nil {
			return nil, err
		}
		if pl != nil {
			r.ProjectLocalPath = localPath
			if pl.Account != "" {
				r.ConfigAccount = pl.Account
			}
			// Project-local developer-id overrides the active Account's: the
			// multi-tenant/agency case (ADR-0015): one credential invited into
			// several orgs, the active org pinned per-repo out of version control.
			if pl.DeveloperID != "" {
				r.DeveloperID = pl.DeveloperID
			}
		}
	}

	return r, nil
}

// LoadFromEnv builds Resolved using fsys's cwd and home for the walk-up
// frame. Production passes OSFS{}; tests prefer Load directly with a
// fixed StartDir / HomeDir.
func LoadFromEnv(ctx context.Context, fsys FS, globalPath string) (*Resolved, error) {
	cwd, err := fsys.Getwd()
	if err != nil {
		return nil, err
	}
	home, err := fsys.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return Load(ctx, fsys, LoadOptions{GlobalPath: globalPath, StartDir: cwd, HomeDir: home})
}

// readProjectShared returns nil, nil if path does not exist.
func readProjectShared(fsys FS, path string) (*projectShared, error) {
	data, err := fsys.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps projectShared
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if len(ps.Account) > 0 {
		return nil, fmt.Errorf("config: %s: field \"account\" is forbidden in committed config (use .gplay/config.local.json or GPLAY_ACCOUNT)", path)
	}
	// A developer-id is credential/org state, not shared repo state: same
	// rule and reason as the account field (ADR-0015).
	if len(ps.DeveloperID) > 0 {
		return nil, fmt.Errorf("config: %s: field \"developerId\" is forbidden in committed config (set it via `gplay auth login --developer-id`, .gplay/config.local.json, or GPLAY_DEVELOPER_ID)", path)
	}
	return &ps, nil
}

// readProjectLocal returns nil, nil if path does not exist.
func readProjectLocal(fsys FS, path string) (*projectLocal, error) {
	data, err := fsys.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pl projectLocal
	if err := json.Unmarshal(data, &pl); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &pl, nil
}
