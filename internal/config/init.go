package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

const (
	gitignoreMarkerStart = "# BEGIN gplay-managed"
	gitignoreMarkerEnd   = "# END gplay-managed"
	gitignoreBody        = `config.local.json
edit-*.json`
)

func Init(_ context.Context, fsys FS, repoRoot, homeDir, pkg string) error {
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
		return exit.Usagef("config init: refusing to initialise at $HOME (%s); run gplay init inside a repo", absRepo)
	}

	gplayDir := filepath.Join(absRepo, ".gplay")
	if err := fsys.MkdirAll(gplayDir, 0o755); err != nil {
		return err
	}
	cfgPath, err := containWrite(absRepo, filepath.Join(gplayDir, "config.json"))
	if err != nil {
		return err
	}
	ignorePath, err := containWrite(absRepo, filepath.Join(gplayDir, ".gitignore"))
	if err != nil {
		return err
	}
	if err := writeConfigJSON(fsys, cfgPath, pkg); err != nil {
		return err
	}
	return ensureGitignore(fsys, ignorePath)
}

func containWrite(repoRoot, path string) (string, error) {
	if root, rootErr := pathguard.Root(repoRoot); rootErr == nil {
		return pathguard.ContainWrite(root, path)
	}
	return path, nil
}

func validatePackage(pkg string) error {
	if pkg == "" {
		return exit.Usagef("config init: package is required (e.g. --package com.example.myapp)")
	}
	if !strings.Contains(pkg, ".") {
		return exit.Usagef("config init: package %q does not look like a valid Android package name (must contain a '.')", pkg)
	}
	return nil
}

func writeConfigJSON(fsys FS, path, pkg string) error {
	payload := struct {
		Package string `json:"package"`
	}{Package: pkg}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return fsys.WriteFile(path, append(data, '\n'), 0o644)
}

func ensureGitignore(fsys FS, path string) error {
	managed := gitignoreMarkerStart + "\n" + gitignoreBody + "\n" + gitignoreMarkerEnd + "\n"

	existing, err := fsys.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err != nil {
		return fsys.WriteFile(path, []byte(managed), 0o644)
	}

	updated, ok := replaceManagedBlock(string(existing), managed)
	if !ok {
		buf := string(existing)
		if len(buf) > 0 && !strings.HasSuffix(buf, "\n") {
			buf += "\n"
		}
		updated = buf + managed
	}
	return fsys.WriteFile(path, []byte(updated), 0o644)
}

func replaceManagedBlock(existing, newBlock string) (string, bool) {
	startIdx := strings.Index(existing, gitignoreMarkerStart)
	if startIdx == -1 {
		return existing, false
	}
	endIdx := strings.Index(existing[startIdx:], gitignoreMarkerEnd)
	if endIdx == -1 {
		return existing[:startIdx] + newBlock, true
	}
	endIdx += startIdx + len(gitignoreMarkerEnd)
	if endIdx < len(existing) && existing[endIdx] == '\n' {
		endIdx++
	}
	return existing[:startIdx] + newBlock + existing[endIdx:], true
}
