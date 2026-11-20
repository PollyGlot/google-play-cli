package editpin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

type Pin struct {
	EditID  string `json:"editId"`
	Package string `json:"package"`
}

func FileName(pkg string) string {
	return "edit-" + pkg + ".json"
}

func Path(gplayDir, pkg string) string {
	return filepath.Join(gplayDir, FileName(pkg))
}

func pinPath(gplayDir, pkg string) (path, root string, err error) {
	if err := pathguard.Segment("package", pkg); err != nil {
		return "", "", err
	}
	root, err = pathguard.Root(gplayDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Path(gplayDir, pkg), "", nil
		}
		return "", "", err
	}
	path, err = pathguard.Contain(root, filepath.Join(root, FileName(pkg)))
	if err != nil {
		return "", "", err
	}
	return path, root, nil
}

func Lookup(fsys config.FS, gplayDir, pkg string) (Pin, bool, error) {
	path, _, err := pinPath(gplayDir, pkg)
	if err != nil {
		return Pin{}, false, err
	}
	data, err := fsys.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Pin{}, false, nil
		}
		return Pin{}, false, err
	}
	var p Pin
	if err := json.Unmarshal(data, &p); err != nil {
		return Pin{}, false, &CorruptError{Path: path, Package: pkg, Reason: err.Error()}
	}
	p.EditID = strings.TrimSpace(p.EditID)
	if p.EditID == "" {
		return Pin{}, false, &CorruptError{Path: path, Package: pkg, Reason: "missing editId"}
	}
	if p.Package != "" && p.Package != pkg {
		return Pin{}, false, &CorruptError{Path: path, Package: pkg, Reason: fmt.Sprintf("package mismatch (%q != %q)", p.Package, pkg)}
	}
	return p, true, nil
}

type CorruptError struct {
	Path    string
	Package string
	Reason  string
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("editpin: %s: %s: run `gplay edits discard --package %s` to remove the unreadable pin", e.Path, e.Reason, e.Package)
}

func (e *CorruptError) ExitCode() int { return 60 }

func Write(fsys config.FS, gplayDir, pkg, editID string) error {
	if err := pathguard.Segment("package", pkg); err != nil {
		return err
	}
	if err := fsys.MkdirAll(gplayDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(Pin{EditID: editID, Package: pkg}, "", "  ")
	if err != nil {
		return err
	}
	path, root, err := pinPath(gplayDir, pkg)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if root != "" {
		if tmp, err = pathguard.ContainWrite(root, tmp); err != nil {
			return err
		}
	}
	if err := fsys.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		_ = fsys.Remove(tmp)
		return err
	}
	if err := fsys.Rename(tmp, path); err != nil {
		_ = fsys.Remove(tmp)
		return err
	}
	return nil
}

func Clear(fsys config.FS, gplayDir, pkg string) error {
	path, _, err := pinPath(gplayDir, pkg)
	if err != nil {
		return err
	}
	if err := fsys.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
