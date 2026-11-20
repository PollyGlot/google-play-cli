package schemaindex_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

var (
	snapshotPaths = []string{
		filepath.Join("..", "..", "docs", "discovery", "androidpublisher_v3.json"),
		filepath.Join("..", "..", "docs", "discovery", "playdeveloperreporting_v1beta1.json"),
		filepath.Join("..", "..", "docs", "discovery", "gamesConfiguration_v1configuration.json"),
		filepath.Join("..", "..", "docs", "discovery", "playcustomapp_v1.json"),
		filepath.Join("..", "..", "docs", "discovery", "gamesManagement_v1management.json"),
	}
	indexPath = filepath.Join("schema_index.json")
)

const methodFloor = 120

var sentinelMethods = []string{
	"androidpublisher.edits.commit",
	"androidpublisher.edits.insert",
	"androidpublisher.edits.tracks.update",
	"androidpublisher.reviews.list",
	"playdeveloperreporting.vitals.crashrate.query",
	"gamesConfiguration.achievementConfigurations.list",
	"playcustomapp.accounts.customApps.create",
	"gamesManagement.players.hide",
}

func TestEmbeddedIndexMatchesSnapshot(t *testing.T) {
	var snapshots [][]byte
	for _, p := range snapshotPaths {
		snap, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read snapshot %s: %v (run `make discovery-update`)", p, err)
		}
		snapshots = append(snapshots, snap)
	}
	want, err := schemaindex.RenderAll(snapshots)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	got, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v (run `make schema-index-update`)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale: run `make schema-index-update` and commit the result", indexPath)
	}
}

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
