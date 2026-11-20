package keystore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

var ErrNotFound = errors.New("keystore: credential not found")

type Backend interface {
	Save(ctx context.Context, name string, data []byte) error
	Load(ctx context.Context, name string) ([]byte, error)
	Delete(ctx context.Context, name string) error
	List(ctx context.Context) ([]string, error)
}

type FileBackend struct {
	root string
}

func NewFileBackend(dir string) *FileBackend {
	return &FileBackend{root: dir}
}

const fileSuffix = ".json"

func validName(name string) error {
	return pathguard.Segment("Account name", name)
}

func (b *FileBackend) path(name string) string {
	return filepath.Join(b.root, name+fileSuffix)
}

func (b *FileBackend) Path(name string) string {
	return b.path(name)
}

func (b *FileBackend) Save(_ context.Context, name string, data []byte) error {
	if err := validName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(b.root, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(b.root, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(b.path(name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

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

func WarnFileFallback(w io.Writer, path string) {
	_, _ = fmt.Fprintf(w, "warning: OS keyring unavailable; credential stored in plaintext file %s (mode 0600)\n", path)
}

func WarnStrayFiles(ctx context.Context, w io.Writer, root string) {
	names, err := NewFileBackend(root).List(ctx)
	if err != nil || len(names) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "warning: keyring is active but %d plaintext credential file(s) remain in %s (%s); they are not read while the keyring is reachable, delete them once unneeded\n",
		len(names), root, strings.Join(names, ", "))
}
