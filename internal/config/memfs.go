package config

import (
	"errors"
	"io/fs"
	"path"
	"sync"
	"time"
)

// MemFS is an in-memory FS for tests. It implements only the operations
// gplay actually uses (see internal/config.FS). Directories are tracked
// implicitly by file paths; explicit MkdirAll is a no-op that succeeds
// after recording the directory so Stat on a dir-only path still works.
//
// MemFS is safe for parallel access from a single test goroutine; it is
// NOT safe across goroutines without external synchronisation.
type MemFS struct {
	mu      sync.Mutex
	files   map[string]memFile
	dirs    map[string]struct{}
	cwd     string
	homeDir string
}

type memFile struct {
	data []byte
	perm fs.FileMode
	mod  time.Time
}

// NewMemFS returns an empty in-memory FS with the given Getwd() /
// UserHomeDir() seeds. Both default to "/" when empty so tests don't
// have to set them when the cascade doesn't read them.
func NewMemFS(cwd, home string) *MemFS {
	if cwd == "" {
		cwd = "/"
	}
	if home == "" {
		home = "/"
	}
	return &MemFS{
		files:   map[string]memFile{},
		dirs:    map[string]struct{}{},
		cwd:     cwd,
		homeDir: home,
	}
}

func (m *MemFS) ReadFile(name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return append([]byte(nil), f.data...), nil
}

func (m *MemFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = memFile{
		data: append([]byte(nil), data...),
		perm: perm,
		mod:  time.Now(),
	}
	// Record every parent directory so ReadDir + Stat see them.
	for d := path.Dir(name); d != "." && d != "/"; d = path.Dir(d) {
		m.dirs[d] = struct{}{}
	}
	return nil
}

func (m *MemFS) MkdirAll(p string, _ fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for d := p; d != "." && d != "/"; d = path.Dir(d) {
		m.dirs[d] = struct{}{}
	}
	return nil
}

type memFileInfo struct {
	name  string
	size  int64
	perm  fs.FileMode
	mod   time.Time
	isDir bool
}

func (m memFileInfo) Name() string       { return m.name }
func (m memFileInfo) Size() int64        { return m.size }
func (m memFileInfo) Mode() fs.FileMode  { return m.perm }
func (m memFileInfo) ModTime() time.Time { return m.mod }
func (m memFileInfo) IsDir() bool        { return m.isDir }
func (m memFileInfo) Sys() any           { return nil }

func (m *MemFS) Stat(name string) (fs.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.files[name]; ok {
		return memFileInfo{
			name: path.Base(name), size: int64(len(f.data)), perm: f.perm, mod: f.mod,
		}, nil
	}
	if _, ok := m.dirs[name]; ok {
		return memFileInfo{name: path.Base(name), isDir: true, perm: fs.ModeDir | 0o700}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

func (m *MemFS) Getwd() (string, error) { return m.cwd, nil }

func (m *MemFS) UserHomeDir() (string, error) {
	if m.homeDir == "" {
		return "", errors.New("memfs: no home dir set")
	}
	return m.homeDir, nil
}

// Rename moves a file between two paths in-memory. Mirrors os.Rename's
// semantics: missing source is ErrNotExist; overwriting an existing
// destination succeeds silently.
func (m *MemFS) Rename(oldpath, newpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[oldpath]
	if !ok {
		return &fs.PathError{Op: "rename", Path: oldpath, Err: fs.ErrNotExist}
	}
	m.files[newpath] = f
	delete(m.files, oldpath)
	for d := path.Dir(newpath); d != "." && d != "/"; d = path.Dir(d) {
		m.dirs[d] = struct{}{}
	}
	return nil
}

// Remove deletes a file. Missing files surface as fs.ErrNotExist:
// matches os.Remove so callers using errors.Is(_, fs.ErrNotExist) keep
// working under MemFS.
func (m *MemFS) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[name]; !ok {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrNotExist}
	}
	delete(m.files, name)
	return nil
}
