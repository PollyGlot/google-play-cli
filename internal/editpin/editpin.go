// Package editpin persists the open *explicit* Edit ID for a package to
// .gplay/edit-<package>.json — the file `gplay edits begin` writes and every
// write command consults to reuse an open Edit instead of opening its own
// (docs/DESIGN.md §4 / CONTEXT.md "Edit"). The directory is the project's
// .gplay/ (the same dir that holds config.json), which `gplay init` already
// gitignores for edit-*.json (transient, per-machine state). Reads and writes
// go through config.FS so callers and tests stay filesystem-seamed.
package editpin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

// Pin is the on-disk shape of .gplay/edit-<package>.json: the open Edit ID and
// the package it belongs to (the latter is informational — the filename already
// carries the package — but makes a hand-inspected file self-describing).
type Pin struct {
	EditID  string `json:"editId"`
	Package string `json:"package"`
}

// FileName returns the pin filename for pkg ("edit-<pkg>.json"). Android
// package names are dot-separated identifiers, so they are filesystem-safe
// verbatim; the name matches the edit-*.json glob `gplay init` gitignores.
func FileName(pkg string) string {
	return "edit-" + pkg + ".json"
}

// Path returns <gplayDir>/edit-<pkg>.json.
func Path(gplayDir, pkg string) string {
	return filepath.Join(gplayDir, FileName(pkg))
}

// Lookup reads the pin for pkg from gplayDir. It returns ok=false (with a nil
// error) when no pin file exists — the common implicit-mode case a write
// command treats as "open your own Edit". A present-but-corrupt file, or one
// missing its editId, is an error: a broken pin must surface, not silently fall
// back to opening a fresh (conflicting) Edit while the real one stays open.
func Lookup(fsys config.FS, gplayDir, pkg string) (Pin, bool, error) {
	path := Path(gplayDir, pkg)
	data, err := fsys.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Pin{}, false, nil
		}
		return Pin{}, false, err
	}
	var p Pin
	if err := json.Unmarshal(data, &p); err != nil {
		return Pin{}, false, fmt.Errorf("editpin: %s: %w", path, err)
	}
	if strings.TrimSpace(p.EditID) == "" {
		return Pin{}, false, fmt.Errorf("editpin: %s: missing editId", path)
	}
	return p, true, nil
}

// Write persists the pin for pkg to gplayDir, creating gplayDir if it does not
// yet exist (mirrors config.Init's 0755/0644 modes).
func Write(fsys config.FS, gplayDir, pkg, editID string) error {
	if err := fsys.MkdirAll(gplayDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(Pin{EditID: editID, Package: pkg}, "", "  ")
	if err != nil {
		return err
	}
	return fsys.WriteFile(Path(gplayDir, pkg), append(data, '\n'), 0o644)
}

// Clear removes the pin file for pkg. A missing file is not an error, so the
// local side of `gplay edits commit`/`discard` is idempotent (and `discard`
// can clear the pin even when the Edit has already expired server-side).
func Clear(fsys config.FS, gplayDir, pkg string) error {
	if err := fsys.Remove(Path(gplayDir, pkg)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
