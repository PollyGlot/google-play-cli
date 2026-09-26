// Package keystore stores service-account JSON credentials by name. The
// foundation slice ships a file backend only; the OS keystore (Keychain,
// Credential Manager, Secret Service) lands in a follow-up slice behind the
// same interface.
package keystore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

// ErrNotFound is returned by Load and Delete when no credential is stored
// under the given name.
var ErrNotFound = errors.New("keystore: credential not found")

// Backend is the storage contract. Implementations must persist arbitrary
// byte blobs (the SA JSON) under human-friendly names. ctx is threaded
// for future cancellation support: today's backends are synchronous
// but a remote/HSM-backed backend would honour it.
type Backend interface {
	Save(ctx context.Context, name string, data []byte) error
	Load(ctx context.Context, name string) ([]byte, error)
	Delete(ctx context.Context, name string) error
	List(ctx context.Context) ([]string, error)
}

// FileBackend persists credentials as `<root>/<name>.json` with mode 0600.
type FileBackend struct {
	root string
}

// NewFileBackend returns a backend rooted at dir. The directory is created
// lazily on the first Save.
func NewFileBackend(dir string) *FileBackend {
	return &FileBackend{root: dir}
}

const fileSuffix = ".json"

// validName refuses an Account name that is not one plain path component,
// before Save, Load or Delete joins it into a path. The name reaches the file
// backend from `auth login --name`, `--account`, GPLAY_ACCOUNT and a repo's
// .gplay/config.local.json, and a `../x` from any of them would read, write or
// delete a file outside root (#603). The OS keyring backend keys items by name
// without touching the filesystem, so it keeps accepting any name.
func validName(name string) error {
	return pathguard.Segment("Account name", name)
}

func (b *FileBackend) path(name string) string {
	return filepath.Join(b.root, name+fileSuffix)
}

// Save writes data to <root>/<name>.json with mode 0600, creating the parent
// directory if needed.
func (b *FileBackend) Save(_ context.Context, name string, data []byte) error {
	if err := validName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(b.root, 0o700); err != nil {
		return err
	}
	return os.WriteFile(b.path(name), data, 0o600)
}

// Load returns the bytes stored under name, or ErrNotFound.
func (b *FileBackend) Load(_ context.Context, name string) ([]byte, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(b.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}

// Delete removes the credential. Returns ErrNotFound if absent.
//
// A name validName refuses is reported as ErrNotFound, not as an error: Save
// never stores one, so there is nothing under it to delete, and a caller that
// sweeps every store (logout deletes from the keyring AND the file backend)
// must be able to finish for a keyring-held Account whose name has a slash.
func (b *FileBackend) Delete(_ context.Context, name string) error {
	if validName(name) != nil {
		return ErrNotFound
	}
	err := os.Remove(b.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// List returns the names currently stored (without the .json suffix).
func (b *FileBackend) List(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(b.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, fileSuffix) {
			continue
		}
		names = append(names, strings.TrimSuffix(n, fileSuffix))
	}
	return names, nil
}
