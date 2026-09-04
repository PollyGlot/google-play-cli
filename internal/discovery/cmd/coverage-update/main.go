// Command coverage-update renders docs/COVERAGE.md from the committed
// Discovery existence index and the API method registry (slice #514). It is
// OFFLINE: it makes no network call; `make discovery-update` is the separate,
// networked step that refreshes the index this reads.
//
// It is a thin wrapper over internal/coveragedoc, the same Render the offline
// freshness test re-runs, so a re-render is byte-equal to the committed file.
// Invoke via `make coverage-update`.
//
// This command is NOT part of the shipped gplay binary; it is maintenance
// tooling run by a human (or by the Discovery triage bot) on demand.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PollyGlot/google-play-cli/internal/coveragedoc"
)

func main() {
	paths := flag.String("paths", filepath.Join("docs", "discovery", "paths.txt"), "path to the committed Discovery existence index")
	out := flag.String("out", filepath.Join("docs", "COVERAGE.md"), "path to write the coverage matrix to")
	flag.Parse()

	if err := run(*paths, *out); err != nil {
		fmt.Fprintln(os.Stderr, "coverage-update:", err)
		os.Exit(1)
	}
}

func run(pathsFile, out string) error {
	raw, err := os.ReadFile(pathsFile)
	if err != nil {
		return fmt.Errorf("read %s: %w (run `make discovery-update` first)", pathsFile, err)
	}

	doc, err := coveragedoc.Render(raw)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, doc, 0o644); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", out, len(doc))
	return nil
}
