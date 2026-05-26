package config

import (
	"io/fs"
	"os"
)

// FS is the slice of os.* the config layer touches. Production wires
// OSFS{}; tests can pass an in-memory fake to keep `t.TempDir()` out
// of unit runs. The interface only lists the operations gplay
// actually uses; add a method here when a new caller needs it.
//
// Rename + Remove support the atomic write-tmp+rename pattern in
// Global.Save; both must work on the same logical filesystem as
// WriteFile so the rename is a metadata-only operation.
type FS interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Stat(name string) (fs.FileInfo, error)
	Getwd() (string, error)
	UserHomeDir() (string, error)
	Rename(oldpath, newpath string) error
	Remove(name string) error
}

// OSFS is the production FS — it forwards every call to the os package.
// Stateless (no fields), safe to pass by value.
type OSFS struct{}

func (OSFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }
func (OSFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (OSFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }
func (OSFS) Stat(name string) (fs.FileInfo, error)        { return os.Stat(name) }
func (OSFS) Getwd() (string, error)                       { return os.Getwd() }
func (OSFS) UserHomeDir() (string, error)                 { return os.UserHomeDir() }
func (OSFS) Rename(oldpath, newpath string) error         { return os.Rename(oldpath, newpath) }
func (OSFS) Remove(name string) error                     { return os.Remove(name) }
