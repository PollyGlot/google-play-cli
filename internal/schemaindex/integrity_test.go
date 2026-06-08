package schemaindex_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// Paths relative to this package (go test runs with the package dir as cwd).
var (
	snapshotPath = filepath.Join("..", "..", "docs", "discovery", "androidpublisher_v3.json")
	indexPath    = filepath.Join("..", "..", "commands", "schema", "schema_index.json")
)

// methodFloor is a deliberately loose lower bound: the androidpublisher v3
// surface is ~137 methods. A re-derivation dropping below this means a
// truncated or wrong snapshot, not a normal API change.
const methodFloor = 120

// sentinelMethods must survive any regeneration — a cheap guard against a
// truncated or wrong-service index.
var sentinelMethods = []string{
	"androidpublisher.edits.commit",
	"androidpublisher.edits.insert",
	"androidpublisher.edits.tracks.update",
	"androidpublisher.reviews.list",
}

// TestEmbeddedIndexMatchesSnapshot is the freshness gate (D10): the committed
// index must be byte-equal to a re-derivation from the committed snapshot. A
// stale index left behind after a snapshot refresh — or a hand-edit — fails
// here, with no network and no new CI step.
func TestEmbeddedIndexMatchesSnapshot(t *testing.T) {
	snapshot, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot: %v (run `make discovery-update`)", err)
	}
	want, err := schemaindex.Render(snapshot)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v (run `make schema-index-update`)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale — run `make schema-index-update` and commit the result", indexPath)
	}
}

// TestEmbeddedIndexSentinels asserts the committed index carries the known
// method ids, the Track schema, and at least methodFloor methods.
func TestEmbeddedIndexSentinels(t *testing.T) {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	idx, err := schemaindex.Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(idx.Methods) < methodFloor {
		t.Errorf("index has %d methods, want >= %d", len(idx.Methods), methodFloor)
	}
	for _, id := range sentinelMethods {
		if _, ok := idx.Methods[id]; !ok {
			t.Errorf("index missing sentinel method %q", id)
		}
	}
	if _, ok := idx.Schemas["Track"]; !ok {
		t.Error("index missing sentinel schema Track")
	}
}
