package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// gitignoreSnippet is the line set Init writes (or appends, if missing) to
// .gplay/.gitignore. The marker lines guard the auto-managed block so a
// rerun does not duplicate them — and so any rules the user added outside
// the block survive untouched.
const (
	gitignoreMarkerStart = "# BEGIN gplay-managed"
	gitignoreMarkerEnd   = "# END gplay-managed"
	gitignoreBody        = `config.local.json
edit-*.json`
)

// Init writes <repoRoot>/.gplay/config.json with the given package pin and
// a sibling .gplay/.gitignore. It refuses to run when repoRoot equals
// homeDir, preventing an accidental ~/.gplay/config.json from being
// picked up via walk-up as if it were a project pin.
//
// The .gitignore write is idempotent: the gplay-managed block is bounded
// by marker lines so a rerun replaces only that block, leaving any
// user-added rules untouched.
//
// config.json is rewritten unconditionally so calling Init twice with a
// different package updates the pin.
func Init(repoRoot, homeDir, pkg string) error {
	if repoRoot == "" {
		return fmt.Errorf("config init: repoRoot is empty")
	}
	if err := validatePackage(pkg); err != nil {
		return err
	}
	absRepo, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}
	absHome, err := filepath.Abs(homeDir)
	if err != nil {
		return err
	}
	if absRepo == absHome {
		return fmt.Errorf("config init: refusing to initialise at $HOME (%s) — a config there would be picked up by every repo's walk-up. Run gplay init inside the repo you actually want to pin.", absRepo)
	}

	gplayDir := filepath.Join(absRepo, ".gplay")
	if err := os.MkdirAll(gplayDir, 0o755); err != nil {
		return err
	}
	if err := writeConfigJSON(filepath.Join(gplayDir, "config.json"), pkg); err != nil {
		return err
	}
	return ensureGitignore(filepath.Join(gplayDir, ".gitignore"))
}

func validatePackage(pkg string) error {
	if pkg == "" {
		return fmt.Errorf("config init: package is required (e.g. --package com.example.myapp)")
	}
	if !strings.Contains(pkg, ".") {
		return fmt.Errorf("config init: package %q does not look like a valid Android package name (must contain a '.')", pkg)
	}
	return nil
}

func writeConfigJSON(path, pkg string) error {
	payload := struct {
		Package string `json:"package"`
	}{Package: pkg}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// ensureGitignore writes the gplay-managed block into path. If the file
// already exists, the existing managed block (between markers) is
// replaced and the user-authored content around it is preserved. If no
// managed block exists, one is appended.
func ensureGitignore(path string) error {
	managed := gitignoreMarkerStart + "\n" + gitignoreBody + "\n" + gitignoreMarkerEnd + "\n"

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err != nil { // file doesn't exist — write fresh
		return os.WriteFile(path, []byte(managed), 0o644)
	}

	updated, ok := replaceManagedBlock(string(existing), managed)
	if !ok {
		// No marker block yet: append (ensuring a newline before our block).
		buf := string(existing)
		if len(buf) > 0 && !strings.HasSuffix(buf, "\n") {
			buf += "\n"
		}
		updated = buf + managed
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

// replaceManagedBlock swaps the existing gplay-managed block (delimited
// by gitignoreMarkerStart/End) with newBlock. Returns ok=false when no
// existing block is found, signalling that the caller should append.
func replaceManagedBlock(existing, newBlock string) (string, bool) {
	startIdx := strings.Index(existing, gitignoreMarkerStart)
	if startIdx == -1 {
		return existing, false
	}
	endIdx := strings.Index(existing[startIdx:], gitignoreMarkerEnd)
	if endIdx == -1 {
		// Start marker without end marker — replace from start marker
		// through end of file to recover.
		return existing[:startIdx] + newBlock, true
	}
	endIdx += startIdx + len(gitignoreMarkerEnd)
	// Consume the newline after the end marker too, if any.
	if endIdx < len(existing) && existing[endIdx] == '\n' {
		endIdx++
	}
	return existing[:startIdx] + newBlock + existing[endIdx:], true
}
