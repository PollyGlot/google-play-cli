// Package apiregistry_test anchors the declarative registry of API methods
// gplay calls to the offline Discovery artefacts. Everything here reads
// committed files: no network, no auth, no binary.
package apiregistry_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// repoDocs is docs/ relative to this package (go test runs with the package
// directory as the working directory).
var repoDocs = filepath.Join("..", "..", "docs")

// TestRegistryIsWellFormed guards the registry's own shape: no duplicate method
// id, and every entry names at least one command. A duplicate would silently
// hide one of the two mappings from the triage bot.
func TestRegistryIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range apiregistry.Entries() {
		if e.MethodID == "" {
			t.Errorf("registry entry with an empty method id")
			continue
		}
		if seen[e.MethodID] {
			t.Errorf("method %q is registered twice: merge the two entries' commands", e.MethodID)
		}
		seen[e.MethodID] = true
		if len(e.Commands) == 0 {
			t.Errorf("method %q: registered with no command; a method nothing calls does not belong here", e.MethodID)
		}
		for _, c := range e.Commands {
			if strings.TrimSpace(c) == "" {
				t.Errorf("method %q: empty command name", e.MethodID)
			}
			if strings.HasPrefix(c, "gplay ") {
				t.Errorf("method %q: command %q must not repeat the binary name", e.MethodID, c)
			}
		}
	}
}

// TestRegisteredMethodsExistInSchemaIndex is the removal alarm: every method a
// shipped command calls must still be in the Schema index compiled into the
// binary. When Google drops a method, the Monday Discovery refresh regenerates
// the index and this test names both the method and the command that breaks.
func TestRegisteredMethodsExistInSchemaIndex(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	for _, e := range apiregistry.Entries() {
		if _, ok := idx.Methods[e.MethodID]; !ok {
			t.Errorf("method %q is gone from the Schema index but `gplay %s` still calls it: "+
				"the API dropped it, or the index is stale (run `make schema-index-update`)",
				e.MethodID, strings.Join(e.Commands, "`, `gplay "))
		}
	}
}

// TestRegisteredMethodsExistInPathsIndex is the same alarm read from the other
// artefact: paths.txt is the existence index the Discovery refresh diffs to
// classify a run as `surface`. Checking it too means a `surface` refresh that
// deletes a method gplay calls goes red even before the Schema index is
// regenerated.
func TestRegisteredMethodsExistInPathsIndex(t *testing.T) {
	known := methodIDsFromPaths(t)
	for _, e := range apiregistry.Entries() {
		if !known[e.MethodID] {
			t.Errorf("method %q is absent from docs/discovery/paths.txt but `gplay %s` still calls it: "+
				"Google removed it, or the id is a typo",
				e.MethodID, strings.Join(e.Commands, "`, `gplay "))
		}
	}
}

// TestRegisteredMethodsAreNotDeprecated is the deprecation alarm. Discovery
// marks a doomed method `"deprecated": true` months before it disappears; that
// window is exactly when a maintainer wants a ticket, not a broken release.
func TestRegisteredMethodsAreNotDeprecated(t *testing.T) {
	deprecated := deprecatedMethodIDs(t)
	for _, e := range apiregistry.Entries() {
		if deprecated[e.MethodID] {
			t.Errorf("method %q is marked deprecated in the Discovery snapshot but `gplay %s` still calls it: "+
				"plan the migration before Google removes it",
				e.MethodID, strings.Join(e.Commands, "`, `gplay "))
		}
	}
}

// --- helpers ---------------------------------------------------------------

// methodIDsFromPaths reads the committed existence index. Each line is
// `id⇥verb⇥path` (docs/discovery/README.md).
func methodIDsFromPaths(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open(filepath.Join(repoDocs, "discovery", "paths.txt"))
	if err != nil {
		t.Fatalf("open paths.txt: %v (run `make discovery-update`)", err)
	}
	defer func() { _ = f.Close() }()

	ids := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if id, _, ok := strings.Cut(sc.Text(), "\t"); ok {
			ids[id] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read paths.txt: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("paths.txt parsed empty: the format changed, fix this parser")
	}
	return ids
}

// deprecatedMethodIDs walks every committed snapshot and collects the ids of
// methods carrying `"deprecated": true`. The walk is generic (map[string]any)
// rather than typed because schemaindex deliberately drops the flag: the index
// is a trimmed projection, the snapshot is the full document.
func deprecatedMethodIDs(t *testing.T) map[string]bool {
	t.Helper()
	dir := filepath.Join(repoDocs, "discovery")
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no Discovery snapshot under %s: %v", dir, err)
	}

	out := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			id, hasID := node["id"].(string)
			dep, _ := node["deprecated"].(bool)
			if hasID && dep {
				if _, isMethod := node["httpMethod"].(string); isMethod {
					out[id] = true
				}
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}

	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		walk(doc)
	}
	return out
}
