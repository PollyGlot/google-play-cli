// Command schema-index-update derives the embedded Schema index
// (commands/schema/schema_index.json) from the committed Discovery snapshot
// under docs/discovery/ (issue #200). It is OFFLINE — it makes no network call;
// `make discovery-update` is the separate, networked step that refreshes the
// snapshot this reads.
//
// It is a thin wrapper over the importable internal/schemaindex package — the
// same Derive/Render logic the offline integrity test re-runs — so a
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

// service is the single v1 surface: androidpublisher v3 (D2). The index is
// keyed by native method id, whose leading segment is the service
// discriminator, so adding a second service later (vitals, #49) is a data add,
// not a schema change.
var service = discovery.Service{Name: "androidpublisher", Host: "androidpublisher.googleapis.com", Version: "v3"}

func main() {
	snapDir := flag.String("snapshots", filepath.Join("docs", "discovery"), "directory the committed Discovery snapshot is read from")
	out := flag.String("out", filepath.Join("commands", "schema", "schema_index.json"), "path to write the derived Schema index to")
	flag.Parse()

	if err := run(*snapDir, *out); err != nil {
		fmt.Fprintln(os.Stderr, "schema-index-update:", err)
		os.Exit(1)
	}
}

func run(snapDir, out string) error {
	snapPath := filepath.Join(snapDir, service.SnapshotFilename())
	snapshot, err := os.ReadFile(snapPath)
	if err != nil {
		return fmt.Errorf("read snapshot %s: %w (run `make discovery-update` first)", snapPath, err)
	}

	index, err := schemaindex.Render(snapshot)
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
