// Package apiregistry_test anchors the declarative registry of API methods
// gplay calls to the offline Discovery artefacts. Everything here reads
// committed files: no network, no auth, no binary.
package apiregistry_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// TestCoverageShippedRowsAreRegistered keeps docs/COVERAGE.md honest: a surface
// marked ✅ claims a shipped command covers it, so at least one registered
// method must belong to that surface. Flipping a row to ✅ without wiring the
// registry now fails here, and so does a surface the CLI stopped calling.
func TestCoverageShippedRowsAreRegistered(t *testing.T) {
	registered := make([]string, 0, len(apiregistry.Entries()))
	for _, e := range apiregistry.Entries() {
		registered = append(registered, e.MethodID)
	}
	sort.Strings(registered)

	rows := shippedCoverageSurfaces(t)
	if len(rows) < 20 {
		t.Fatalf("only %d shipped rows parsed out of docs/COVERAGE.md: the table shape changed, fix this parser", len(rows))
	}
	for _, r := range rows {
		if reason, ok := apiregistry.CoverageExceptions[r.surface]; ok {
			t.Logf("COVERAGE row %q: documented exception (%s)", r.surface, reason)
			continue
		}
		prefix := r.service + "." + r.surface
		found := false
		for _, id := range registered {
			if id == prefix || strings.HasPrefix(id, prefix+".") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("docs/COVERAGE.md marks %q shipped (✅) but no registry entry starts with %q: "+
				"register the methods the command calls, or record why in apiregistry.CoverageExceptions",
				r.surface, prefix)
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

// coverageRow is one ✅ line of a COVERAGE.md table: the surface as spelled in
// the first column, plus the service its table belongs to.
type coverageRow struct {
	service string
	surface string
}

// coverageServices maps a COVERAGE.md table heading to the service prefix its
// method ids carry.
var coverageServices = map[string]string{
	"androidpublisher":       "androidpublisher",
	"playdeveloperreporting": "playdeveloperreporting",
	"playcustomapp":          "playcustomapp",
	"gamesConfiguration":     "gamesConfiguration",
}

var (
	headingRe = regexp.MustCompile("^## `([A-Za-z0-9]+)` ")
	// The first backticked token of the first column is the surface; the rest
	// of the cell is prose (issue links, per-method detail) and partial rows
	// list methods that are NOT shipped, so only the first token is a claim.
	surfaceRe = regexp.MustCompile("^`([A-Za-z0-9_.*]+)`")
)

// shippedCoverageSurfaces parses the ✅ rows of docs/COVERAGE.md. Rows marked
// 🟡/🔵/🔴/⚫️ are claims about work NOT shipped and are ignored; a partially
// shipped row ("13 ✅") still carries a ✅ and is held to the same at-least-one
// standard.
func shippedCoverageSurfaces(t *testing.T) []coverageRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoDocs, "COVERAGE.md"))
	if err != nil {
		t.Fatalf("read COVERAGE.md: %v", err)
	}

	var rows []coverageRow
	service := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			service = coverageServices[m[1]]
			continue
		}
		if service == "" || !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 3 {
			continue
		}
		state := strings.TrimSpace(cells[2])
		if !strings.Contains(state, "✅") {
			continue
		}
		m := surfaceRe.FindStringSubmatch(strings.TrimSpace(cells[0]))
		if m == nil {
			continue
		}
		rows = append(rows, coverageRow{service: service, surface: strings.TrimSuffix(m[1], ".*")})
	}
	return rows
}
