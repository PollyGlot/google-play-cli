package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/discovery"
)

func main() {
	outDir := flag.String("out", filepath.Join("docs", "discovery"), "directory to write snapshots and paths.txt into")
	timeout := flag.Duration("timeout", 60*time.Second, "overall fetch timeout")
	flag.Parse()

	if err := run(*outDir, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "discovery-update:", err)
		os.Exit(1)
	}
}

func run(outDir string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := &http.Client{}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	var snapshots [][]byte
	for _, svc := range discovery.Services {
		fmt.Fprintf(os.Stderr, "fetching %s ...\n", svc.DiscoveryURL())
		raw, err := discovery.Fetch(ctx, client, svc)
		if err != nil {
			return err
		}
		norm, err := discovery.Normalize(raw)
		if err != nil {
			return err
		}
		snapPath := filepath.Join(outDir, svc.SnapshotFilename())
		if err := os.WriteFile(snapPath, norm, 0o644); err != nil {
			return err
		}
		snapshots = append(snapshots, norm)

		rev := revision(norm)
		fmt.Fprintf(os.Stderr, "wrote %s (revision %s)\n", snapPath, rev)
		fmt.Printf("%s revision %s\n", svc.SnapshotFilename(), rev)
	}

	paths, err := discovery.RenderPaths(snapshots)
	if err != nil {
		return err
	}
	pathsFile := filepath.Join(outDir, "paths.txt")
	if err := os.WriteFile(pathsFile, paths, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", pathsFile)
	return nil
}

func revision(norm []byte) string {
	var doc struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(norm, &doc); err != nil || doc.Revision == "" {
		return "unknown"
	}
	return doc.Revision
}
