// Command schema-index-update derives the embedded Schema index
// (internal/schemaindex/schema_index.json) from the committed Discovery
// snapshots under docs/discovery/ (issue #200). It is OFFLINE: it makes no
// network call;
// `make discovery-update` is the separate, networked step that refreshes the
// snapshot this reads.
//
// It is a thin wrapper over the importable internal/schemaindex package: the
// same Derive/Render logic the offline integrity test re-runs, so a
// re-derivation is byte-equal to the committed index. Invoke via
// `make schema-index-update`.
//
// This command is NOT part of the shipped gplay binary; it is maintenance
// tooling run by a human on demand.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PollyGlot/google-play-cli/internal/discovery"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

func main() {
	snapDir := flag.String("snapshots", filepath.Join("docs", "discovery"), "directory the committed Discovery snapshots are read from")
	out := flag.String("out", filepath.Join("internal", "schemaindex", "schema_index.json"), "path to write the derived Schema index to")
	flag.Parse()

	if err := run(*snapDir, *out); err != nil {
		fmt.Fprintln(os.Stderr, "schema-index-update:", err)
		os.Exit(1)
	}
}

// run derives the embedded index from EVERY committed Discovery snapshot
// declared in discovery.Services (androidpublisher v3 + playdeveloperreporting
// v1beta1, #49). The index is keyed by native method id, whose leading segment
// is the service discriminator, so the services merge with no collision.
func run(snapDir, out string) error {
	var snapshots [][]byte
	for _, svc := range discovery.Services {
		snapPath := filepath.Join(snapDir, svc.SnapshotFilename())
		snapshot, err := os.ReadFile(snapPath)
		if err != nil {
			return fmt.Errorf("read snapshot %s: %w (run `make discovery-update` first)", snapPath, err)
		}
		snapshots = append(snapshots, snapshot)
	}

	index, err := schemaindex.RenderAll(snapshots)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, index, 0o644); err != nil {
		return err
	}

	idx, _ := schemaindex.Load(index)
	fmt.Fprintf(os.Stderr, "wrote %s (revision %s, %d methods, %d schemas)\n",
		out, idx.Revision, len(idx.Methods), len(idx.Schemas))
	return nil
}
