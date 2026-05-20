// Package config manages the gplay Account registry persisted to
// $XDG_CONFIG_HOME/gplay/config.json. It tracks the list of known Accounts
// and which one is active. Secrets never live here — they belong in the
// keystore.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Account is the human-friendly registration of a service-account credential.
// The credential bytes themselves live in the keystore, keyed by Name.
type Account struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// Config is the on-disk shape of the registry.
type Config struct {
	Accounts []Account `json:"accounts"`
}

// ErrUnknownAccount is returned when an operation references an account name
// that is not in the registry.
var ErrUnknownAccount = errors.New("config: unknown account")

// LoadOrEmpty reads the config file at path. If the file does not exist, an
// empty Config is returned without error (lazy creation).
func LoadOrEmpty(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config to path with mode 0600, creating the parent
// directory if needed.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// AddAccount upserts an account by name. Calling AddAccount with a name that
// already exists is a no-op (preserves Active flag and ordering).
func (c *Config) AddAccount(name string) {
	for _, a := range c.Accounts {
		if a.Name == name {
			return
		}
	}
	c.Accounts = append(c.Accounts, Account{Name: name})
}

// RemoveAccount deletes an account by name. If the removed account was the
// active one, the registry is left with no active account — callers decide
// whether to choose a new active explicitly. Returns ErrUnknownAccount if
// name is not in the registry.
func (c *Config) RemoveAccount(name string) error {
	for i, a := range c.Accounts {
		if a.Name == name {
			c.Accounts = append(c.Accounts[:i], c.Accounts[i+1:]...)
			return nil
		}
	}
	return ErrUnknownAccount
}

// SetActive marks one account as Active and clears the flag on every other.
// Returns ErrUnknownAccount if name is not in the registry.
func (c *Config) SetActive(name string) error {
	found := false
	for i := range c.Accounts {
		if c.Accounts[i].Name == name {
			c.Accounts[i].Active = true
			found = true
		} else {
			c.Accounts[i].Active = false
		}
	}
	if !found {
		return ErrUnknownAccount
	}
	return nil
}

// Active returns the currently active account, or false if none is set.
func (c *Config) Active() (Account, bool) {
	for _, a := range c.Accounts {
		if a.Active {
			return a, true
		}
	}
	return Account{}, false
}
