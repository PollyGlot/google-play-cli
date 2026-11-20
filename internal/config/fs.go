package config

import (
	"io/fs"
	"os"
)

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
