package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/PollyGlot/google-play-cli/internal/pathguard"
	"github.com/PollyGlot/google-play-cli/internal/walkup"
)

type Account struct {
	Name        string   `json:"name"`
	Active      bool     `json:"active"`
	Packages    []string `json:"packages,omitempty"`
	DeveloperID string   `json:"developerId,omitempty"`
}

type Global struct {
	Accounts []Account `json:"accounts"`
}

type unknownAccountError struct{}

func (unknownAccountError) Error() string { return "config: unknown account" }
func (unknownAccountError) ExitCode() int { return 2 }

var ErrUnknownAccount error = unknownAccountError{}

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

func (g *Global) AddAccount(name string) {
	for _, a := range g.Accounts {
		if a.Name == name {
			return
		}
	}
	g.Accounts = append(g.Accounts, Account{Name: name})
}

func (g *Global) RemoveAccount(name string) error {
	for i, a := range g.Accounts {
		if a.Name == name {
			g.Accounts = append(g.Accounts[:i], g.Accounts[i+1:]...)
			return nil
		}
	}
	return ErrUnknownAccount
}

func (g *Global) SetDeveloperID(name, id string) bool {
	for i := range g.Accounts {
		if g.Accounts[i].Name == name {
			g.Accounts[i].DeveloperID = id
			return true
		}
	}
	return false
}

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

func (g *Global) Active() (Account, bool) {
	for _, a := range g.Accounts {
		if a.Active {
			return a, true
		}
	}
	return Account{}, false
}

type LoadOptions struct {
	GlobalPath string
	StartDir   string
	HomeDir    string
}

type Resolved struct {
	Pin           string
	ConfigAccount string
	Accounts      []Account

	DeveloperID string

	GlobalPath        string
	ProjectSharedPath string
	ProjectLocalPath  string

	ForeignProjectDir string
}

type projectShared struct {
	Package     string          `json:"package"`
	Account     json.RawMessage `json:"account"`
	DeveloperID json.RawMessage `json:"developerId"`
}

type projectLocal struct {
	Account     string `json:"account"`
	DeveloperID string `json:"developerId"`
}

func Load(ctx context.Context, fsys FS, opts LoadOptions) (*Resolved, error) {
	if opts.HomeDir == "" {
		return nil, fmt.Errorf("config: LoadOptions.HomeDir is required")
	}
	r := &Resolved{GlobalPath: opts.GlobalPath}

	g, err := LoadGlobalOrEmpty(ctx, fsys, opts.GlobalPath)
	if err != nil {
		return nil, err
	}
	r.Accounts = g.Accounts
	if a, ok := g.Active(); ok {
		r.ConfigAccount = a.Name
		r.DeveloperID = a.DeveloperID
	}

	repoRoot, err := walkup.FindFileExcluding(opts.StartDir, ".gplay", opts.HomeDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		gplayDir := filepath.Join(repoRoot, ".gplay")
		foreign, err := foreignProject(fsys, gplayDir, opts.StartDir)
		if err != nil {
			return nil, err
		}
		if foreign {
			r.ForeignProjectDir = gplayDir
			return r, nil
		}
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
				if err := pathguard.Segment("the Account name in "+localPath, pl.Account); err != nil {
					return nil, err
				}
				r.ConfigAccount = pl.Account
			}
			if pl.DeveloperID != "" {
				r.DeveloperID = pl.DeveloperID
			}
		}
	}

	return r, nil
}

func foreignProject(fsys FS, gplayDir, startDir string) (bool, error) {
	fi, err := fsys.Stat(gplayDir)
	if err != nil {
		return false, err
	}
	owner, ok := fileOwner(fi)
	if !ok {
		return false, nil
	}
	if fi.Mode().Perm()&0o002 != 0 {
		return true, nil
	}
	if owner == os.Geteuid() {
		return false, nil
	}
	start, err := filepath.Abs(startDir)
	if err != nil {
		return false, err
	}
	sfi, err := fsys.Stat(start)
	if err != nil {
		return false, err
	}
	startOwner, ok := fileOwner(sfi)
	return !ok || startOwner != owner, nil
}

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
	if len(ps.DeveloperID) > 0 {
		return nil, fmt.Errorf("config: %s: field \"developerId\" is forbidden in committed config (set it via `gplay auth login --developer-id`, .gplay/config.local.json, or GPLAY_DEVELOPER_ID)", path)
	}
	return &ps, nil
}

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
