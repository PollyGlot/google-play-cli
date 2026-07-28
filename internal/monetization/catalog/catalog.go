// Package catalog is the on-disk codec of the Monetization catalog
// (CONTEXT.md): a flat directory of <productId>.json files, each one API
// resource in wire format. Read is the input side of `apply`; Write is the
// output side of `pull`. Both keep the invariant that pull-then-apply with no
// edits is a no-op (ADR-0041 §1): Write strips only server-derived noise and
// removes stale catalog files, so the directory always equals the live catalog
// right after a pull.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one product to write: its ID (the filename stem) and the verbatim
// resource bytes.
type Entry struct {
	ProductID string
	Raw       json.RawMessage
}

// Read loads every *.json file of dir as one product each, keyed by product ID.
// The file's productId must match its filename stem — a mismatch is refused
// rather than silently renamed, since the filename is the reconciliation key.
// Non-.json files and subdirectories are ignored (they are not catalog files).
func Read(dir string) (map[string]json.RawMessage, error) {
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read catalog directory %q: %w", dir, err)
	}
	local := map[string]json.RawMessage{}
	for _, de := range dirents {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			return nil, fmt.Errorf("read catalog file %q: %w", de.Name(), err)
		}
		var probe struct {
			ProductID string `json:"productId"`
		}
		if err := json.Unmarshal(b, &probe); err != nil {
			return nil, fmt.Errorf("catalog file %q is not a JSON subscription resource: %w", de.Name(), err)
		}
		id := strings.TrimSuffix(de.Name(), ".json")
		if probe.ProductID != "" && probe.ProductID != id {
			return nil, fmt.Errorf("catalog file %q declares productId %q — the filename stem is the reconciliation key and must match", de.Name(), probe.ProductID)
		}
		local[id] = json.RawMessage(b)
	}
	return local, nil
}

// Write lays down one pretty-printed <productId>.json per entry and removes any
// stale *.json file that no longer matches a live product, so the directory
// mirrors the live catalog after a pull. Server-derived noise that would churn
// review diffs without being declarable state — packageName (redundant with
// --package) and archived (output-only in the current Discovery snapshot) — is
// stripped. Foreign files (non-.json) are never touched. It returns the names
// of the removed files.
func Write(dir string, entries []Entry) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create catalog directory %q: %w", dir, err)
	}
	keep := map[string]struct{}{}
	for _, e := range entries {
		var m map[string]any
		if err := json.Unmarshal(e.Raw, &m); err != nil {
			return nil, fmt.Errorf("decode live subscription %q: %w", e.ProductID, err)
		}
		delete(m, "packageName")
		delete(m, "archived")
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode subscription %q: %w", e.ProductID, err)
		}
		name := e.ProductID + ".json"
		if err := os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644); err != nil {
			return nil, fmt.Errorf("write catalog file %q: %w", name, err)
		}
		keep[name] = struct{}{}
	}
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read catalog directory %q: %w", dir, err)
	}
	var removed []string
	for _, de := range dirents {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		if _, ok := keep[de.Name()]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(dir, de.Name())); err != nil {
			return nil, fmt.Errorf("remove stale catalog file %q: %w", de.Name(), err)
		}
		removed = append(removed, de.Name())
	}
	sort.Strings(removed)
	return removed, nil
}
