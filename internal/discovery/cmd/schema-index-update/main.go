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
