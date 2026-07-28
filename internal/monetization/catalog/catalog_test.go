package catalog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/monetization/catalog"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRead_keysByProductID asserts Read returns one entry per *.json file,
// keyed by the productId that must match the filename.
func TestRead_keysByProductID(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "premium.json", `{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}]}`)
	write(t, dir, "pro.json", `{"productId":"pro"}`)
	write(t, dir, "README.md", `not a catalog file`)

	local, err := catalog.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(local) != 2 {
		t.Fatalf("len = %d, want 2 (non-.json ignored)", len(local))
	}
	if !strings.Contains(string(local["premium"]), `"title"`) {
		t.Errorf("premium raw = %s, want the file body", local["premium"])
	}
}

// TestRead_mismatchedProductID_refuses asserts a file whose productId disagrees
// with its filename is an error, not a silent rename.
func TestRead_mismatchedProductID_refuses(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "premium.json", `{"productId":"other"}`)
	_, err := catalog.Read(dir)
	if err == nil || !strings.Contains(err.Error(), "premium.json") {
		t.Fatalf("err = %v, want a mismatch error naming the file", err)
	}
}

// TestRead_missingDir_refuses asserts a nonexistent directory is an error (the
// caller decides how to exit).
func TestRead_missingDir_refuses(t *testing.T) {
	if _, err := catalog.Read(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("want error for missing dir")
	}
}

// TestWrite_roundTrip asserts Write lays down one pretty-printed <productId>.json
// per entry, strips server-side noise (packageName, archived), removes stale
// .json files, and leaves foreign files alone — so pull then Read is a no-op
// input to the reconciler.
func TestWrite_roundTrip(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "stale.json", `{"productId":"stale"}`)
	write(t, dir, "notes.txt", `keep me`)

	entries := []catalog.Entry{
		{ProductID: "premium", Raw: json.RawMessage(`{"productId":"premium","packageName":"com.example.app","archived":false,"listings":[{"languageCode":"en-US","title":"Premium"}]}`)},
	}
	removed, err := catalog.Write(dir, entries)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(removed) != 1 || removed[0] != "stale.json" {
		t.Errorf("removed = %v, want [stale.json]", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Errorf("foreign file must survive: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "premium.json"))
	if err != nil {
		t.Fatalf("premium.json: %v", err)
	}
	s := string(b)
	if strings.Contains(s, "packageName") || strings.Contains(s, "archived") {
		t.Errorf("file %s must strip packageName/archived", s)
	}
	if !strings.Contains(s, "\n  ") {
		t.Errorf("file %s should be pretty-printed for review diffs", s)
	}

	local, err := catalog.Read(dir)
	if err != nil {
		t.Fatalf("Read after Write: %v", err)
	}
	if len(local) != 1 || local["premium"] == nil {
		t.Fatalf("round-trip lost the entry: %v", local)
	}
}
