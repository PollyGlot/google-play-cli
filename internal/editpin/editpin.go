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
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
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
//
// "Are filesystem-safe" is an assumption about well-formed input, not a
// guarantee: pkg arrives from --package or from .gplay/config.json, and
// nothing upstream forbids a separator or a "..". pinPath below enforces it
// before any I/O (PRD #459 / slice #461).
func FileName(pkg string) string {
	return "edit-" + pkg + ".json"
}

// Path returns <gplayDir>/edit-<pkg>.json. It is the UNCHECKED join, kept for
// rendering a path in an error message; every path used for I/O goes through
// pinPath, which validates first.
func Path(gplayDir, pkg string) string {
	return filepath.Join(gplayDir, FileName(pkg))
}

// pinPath is Path plus containment: the package must be a plain path component
// (so `--package ../../.ssh/authorized_keys` cannot name a file), and the
// resulting path must resolve inside gplayDir (so a symlinked pin file cannot
// redirect the write elsewhere).
//
// It returns the resolved root alongside the path so a caller that derives a
// SIBLING path (Write's `.tmp` staging file) can contain that one too against
// the same root, without paying a second resolution. An empty root means
// gplayDir does not exist yet and nothing could be resolved.
//
// gplayDir is resolved per call rather than cached: the pin operations are
// one-shot (a single read, write, or remove per invocation), so there is no
// walk for a cached root to protect.
func pinPath(gplayDir, pkg string) (path, root string, err error) {
	if err := pathguard.Segment("package", pkg); err != nil {
		return "", "", err
	}
	root, err = pathguard.Root(gplayDir)
	if err != nil {
		// gplayDir does not exist yet: the caller (Write) creates it. Fall back
		// to the lexical join, which the Segment check above has already made
		// safe: with no separator in pkg there is no traversal to perform.
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

// Lookup reads the pin for pkg from gplayDir. It returns ok=false (with a nil
// error) when no pin file exists: the common implicit-mode case a write
// command treats as "open your own Edit". A present-but-corrupt file, or one
// missing its editId, is an error: a broken pin must surface, not silently fall
// back to opening a fresh (conflicting) Edit while the real one stays open.
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
//
// The staging path is contained exactly like the pin itself. It has to be: the
// `edit-*.json` glob `gplay init` gitignores does NOT cover the `.tmp` suffix,
// so `edit-<pkg>.json.tmp` is a name a repo can commit as a symlink, and
// WriteFile would follow it (PRD #459 / slice #461).
func Write(fsys config.FS, gplayDir, pkg, editID string) error {
	// Validate BEFORE MkdirAll: a bad package name must not get as far as
	// creating a directory.
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
		// ContainWrite, not Contain: the staging file is WRITTEN through, so a
		// link aimed back inside .gplay/ (at config.json, say) must be refused
		// too, and only the leaf-symlink check catches that one.
		if tmp, err = pathguard.ContainWrite(root, tmp); err != nil {
			return err
		}
	}
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
	path, _, err := pinPath(gplayDir, pkg)
	if err != nil {
		return err
	}
	if err := fsys.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
