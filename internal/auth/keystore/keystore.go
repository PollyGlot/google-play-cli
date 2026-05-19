// Package keystore stores service-account JSON credentials by name. The
// foundation slice ships a file backend only; the OS keystore (Keychain,
// Credential Manager, Secret Service) lands in a follow-up slice behind the
// same interface.
package keystore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned by Load and Delete when no credential is stored
// under the given name.
var ErrNotFound = errors.New("keystore: credential not found")

// Backend is the storage contract. Implementations must persist arbitrary
// byte blobs (the SA JSON) under human-friendly names.
type Backend interface {
	Save(name string, data []byte) error
	Load(name string) ([]byte, error)
	Delete(name string) error
	List() ([]string, error)
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

func (b *FileBackend) path(name string) string {
	return filepath.Join(b.root, name+fileSuffix)
}

// Save writes data to <root>/<name>.json with mode 0600, creating the parent
// directory if needed.
func (b *FileBackend) Save(name string, data []byte) error {
	if err := os.MkdirAll(b.root, 0o700); err != nil {
		return err
	}
	return os.WriteFile(b.path(name), data, 0o600)
}

// Load returns the bytes stored under name, or ErrNotFound.
func (b *FileBackend) Load(name string) ([]byte, error) {
	data, err := os.ReadFile(b.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}

// Delete removes the credential. Returns ErrNotFound if absent.
func (b *FileBackend) Delete(name string) error {
	err := os.Remove(b.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// List returns the names currently stored (without the .json suffix).
func (b *FileBackend) List() ([]string, error) {
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
