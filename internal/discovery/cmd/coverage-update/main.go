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
