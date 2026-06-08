package discovery_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/discovery"
)

// docsDir is docs/discovery/ relative to this package (go test runs with the
// package directory as the working directory).
var docsDir = filepath.Join("..", "..", "docs", "discovery")

// sentinelMethods are known method ids that must survive any regeneration —
// a cheap guard against a truncated or wrong-service snapshot.
var sentinelMethods = []string{
	"androidpublisher.edits.commit",
	"androidpublisher.edits.insert",
	"androidpublisher.reviews.list",
}

// TestSnapshotIsSelfNormalized asserts each committed snapshot is byte-equal to
// its own re-normalization. This catches a hand-edit or an un-normalized commit
// without any network call — the integrity gate runs inside `go test ./...`.
func TestSnapshotIsSelfNormalized(t *testing.T) {
	for _, svc := range discovery.Services {
		path := filepath.Join(docsDir, svc.SnapshotFilename())
		committed, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v (run `make discovery-update`)", path, err)
		}
		renorm, err := discovery.Normalize(committed)
		if err != nil {
			t.Fatalf("normalize %s: %v", path, err)
		}
		if !bytes.Equal(committed, renorm) {
			t.Errorf("%s is not in normalized form — run `make discovery-update` and commit the result", path)
		}
	}
}

// TestPathsIndexMatchesSnapshot asserts the committed paths.txt is byte-equal to
// a re-derivation from the committed snapshots. This catches a stale index left
// behind after a snapshot refresh.
func TestPathsIndexMatchesSnapshot(t *testing.T) {
	var snapshots [][]byte
	for _, svc := range discovery.Services {
		committed, err := os.ReadFile(filepath.Join(docsDir, svc.SnapshotFilename()))
		if err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		snapshots = append(snapshots, committed)
	}

	want, err := discovery.RenderPaths(snapshots)
	if err != nil {
		t.Fatalf("RenderPaths: %v", err)
	}

	pathsFile := filepath.Join(docsDir, "paths.txt")
	got, err := os.ReadFile(pathsFile)
	if err != nil {
		t.Fatalf("read %s: %v", pathsFile, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale — run `make discovery-update` and commit the result", pathsFile)
	}
}

// TestSnapshotContainsSentinelMethods is the sanity check from #52: the snapshot
// must carry known methods, guarding against a truncated or wrong-service doc.
func TestSnapshotContainsSentinelMethods(t *testing.T) {
	path := filepath.Join(docsDir, discovery.Service{Name: "androidpublisher", Version: "v3"}.SnapshotFilename())
	snap, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, id := range sentinelMethods {
		if !bytes.Contains(snap, []byte(id)) {
			t.Errorf("snapshot %s missing sentinel method %q", path, id)
		}
	}
}
