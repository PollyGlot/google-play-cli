// Package editpin persists the open *explicit* Edit ID for a package to
// .gplay/edit-<package>.json: the file `gplay edits begin` writes and every
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
// the package it belongs to (the latter is informational: the filename already
// carries the package, but makes a hand-inspected file self-describing).
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
// error) when no pin file exists: the common implicit-mode case a write
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
	p.EditID = strings.TrimSpace(p.EditID)
	if p.EditID == "" {
		return Pin{}, false, fmt.Errorf("editpin: %s: missing editId", path)
	}
	// A pin whose embedded package disagrees with the file it was read from is
	// corruption (a copied/renamed file): reject it now rather than later
	// reusing the Edit against the wrong package. An empty Package is tolerated
	// (a minimal/older pin); the filename remains the authority.
	if p.Package != "" && p.Package != pkg {
		return Pin{}, false, fmt.Errorf("editpin: %s: package mismatch (%q != %q)", path, p.Package, pkg)
	}
	return p, true, nil
}

// Write persists the pin for pkg to gplayDir, creating gplayDir if it does not
// yet exist (mirrors config.Init's 0755/0644 modes).
//
// The write is atomic: the bytes go to a sibling `<path>.tmp` first, then a
// rename swaps them onto path (the same tmp+rename pattern as config.Save, and
// why config.FS exposes Rename/Remove). On POSIX (and Windows for same-volume
// renames) rename is atomic, so a crash (SIGKILL, power loss, disk-full)
// mid-write leaves either the OLD pin or the NEW one, never a truncated or
// zero-length file. That matters because Lookup treats a present-but-corrupt
// pin as a FATAL error, which would wedge every later write command AND block
// recovery via `gplay edits begin` (it Lookups before it could overwrite). A
// plain truncate-then-write WriteFile only cleans up on a returned error; a
// killed process never reaches that cleanup.
func Write(fsys config.FS, gplayDir, pkg, editID string) error {
	if err := fsys.MkdirAll(gplayDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(Pin{EditID: editID, Package: pkg}, "", "  ")
	if err != nil {
		return err
	}
	path := Path(gplayDir, pkg)
	tmp := path + ".tmp"
	if err := fsys.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		// Best-effort remove the tmp fragment; never touch the live pin, which
		// a failed fresh write must not disturb.
		_ = fsys.Remove(tmp)
		return err
	}
	if err := fsys.Rename(tmp, path); err != nil {
		_ = fsys.Remove(tmp)
		return err
	}
	return nil
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
