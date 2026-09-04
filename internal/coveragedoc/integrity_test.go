package coveragedoc_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/coveragedoc"
)

// Paths relative to this package (go test runs with the package dir as cwd).
var (
	pathsIndex   = filepath.Join("..", "..", "docs", "discovery", "paths.txt")
	coveragePath = filepath.Join("..", "..", "docs", "COVERAGE.md")
)

// TestCoverageDocMatchesSources is the freshness gate: the committed
// docs/COVERAGE.md must be byte-equal to a re-render from the committed sources
// (paths.txt, the registry, the exclusion list). A row edited by hand, a
// registry entry added without regenerating, or a Discovery refresh left
// half-applied fails here, offline and with no new CI step.
func TestCoverageDocMatchesSources(t *testing.T) {
	raw, err := os.ReadFile(pathsIndex)
	if err != nil {
		t.Fatalf("read paths.txt: %v (run `make discovery-update`)", err)
	}
	want, err := coveragedoc.Render(raw)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := os.ReadFile(coveragePath)
	if err != nil {
		t.Fatalf("read COVERAGE.md: %v (run `make coverage-update`)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("docs/COVERAGE.md is stale or hand-edited: run `make coverage-update` and commit the result")
	}
}

// TestRenderIsDeterministic pins the property the gate above relies on: two
// renders of the same inputs are byte-identical. Without it a map iteration
// creeping into the renderer would turn the gate into a coin flip.
func TestRenderIsDeterministic(t *testing.T) {
	raw, err := os.ReadFile(pathsIndex)
	if err != nil {
		t.Fatalf("read paths.txt: %v", err)
	}
	first, err := coveragedoc.Render(raw)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	second, err := coveragedoc.Render(raw)
	if err != nil {
		t.Fatalf("Render (second): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("Render is not deterministic: two renders of the same input differ")
	}
}

// TestEveryMethodAppearsExactlyOnce is the completeness claim the document
// makes: one row per method of paths.txt, no more, no less, in one of the three
// states. It reads the committed file, so it also catches a row deleted by hand.
func TestEveryMethodAppearsExactlyOnce(t *testing.T) {
	raw, err := os.ReadFile(pathsIndex)
	if err != nil {
		t.Fatalf("read paths.txt: %v", err)
	}
	doc, err := os.ReadFile(coveragePath)
	if err != nil {
		t.Fatalf("read COVERAGE.md: %v", err)
	}

	rows := map[string]int{}
	for _, line := range strings.Split(string(doc), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 3 {
			continue
		}
		id := strings.Trim(strings.TrimSpace(cells[0]), "`")
		state := strings.TrimSpace(cells[1])
		switch state {
		case "✅", "⚫️", "🔴":
			rows[id]++
		default:
			t.Errorf("method %q carries unknown state %q", id, state)
		}
	}

	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		id, _, _ := strings.Cut(line, "\t")
		switch rows[id] {
		case 1:
			delete(rows, id)
		case 0:
			t.Errorf("method %q of paths.txt has no row in COVERAGE.md: run `make coverage-update`", id)
		default:
			t.Errorf("method %q has %d rows in COVERAGE.md, want 1", id, rows[id])
			delete(rows, id)
		}
	}
	for id := range rows {
		t.Errorf("COVERAGE.md has a row for %q, which paths.txt does not know", id)
	}
}

// TestExclusionsAreWellFormed guards the one hand-written input: every
// exclusion names a method once and says why. An exclusion without a reason is
// how "excluded by nature" silently becomes "we did not get to it".
func TestExclusionsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, x := range apiregistry.Exclusions() {
		if x.MethodID == "" {
			t.Error("exclusion with an empty method id")
			continue
		}
		if seen[x.MethodID] {
			t.Errorf("method %q is excluded twice", x.MethodID)
		}
		seen[x.MethodID] = true
		if strings.TrimSpace(x.Reason) == "" {
			t.Errorf("method %q is excluded with no reason", x.MethodID)
		}
	}
	for _, e := range apiregistry.Entries() {
		if seen[e.MethodID] {
			t.Errorf("method %q is both registered and excluded: `gplay %s` calls it",
				e.MethodID, strings.Join(e.Commands, "`, `gplay "))
		}
	}
}
