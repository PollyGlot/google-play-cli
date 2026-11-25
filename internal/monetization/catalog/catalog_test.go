package catalog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/monetization/catalog"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

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

func TestRead_mismatchedProductID_refuses(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "premium.json", `{"productId":"other"}`)
	_, err := catalog.Read(dir)
	if err == nil || !strings.Contains(err.Error(), "premium.json") {
		t.Fatalf("err = %v, want a mismatch error naming the file", err)
	}
}

func TestRead_unreadableFile_isClientSideError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "premium.json")
	write(t, dir, "premium.json", `{"productId":"premium"}`)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("file stays readable (running as root)")
	}
	_, err := catalog.Read(dir)
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d (err %v), want 20", got, err)
	}
	if !strings.Contains(err.Error(), "premium.json") {
		t.Errorf("err %q should name the file", err)
	}
}

func TestRead_missingDir_refuses(t *testing.T) {
	if _, err := catalog.Read(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("want error for missing dir")
	}
}

func TestWrite_replacesSymlinkWithoutFollowing(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "target.json")
	if err := os.WriteFile(target, []byte(`untouched`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "premium.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := catalog.Write(dir, []catalog.Entry{{ProductID: "premium", Raw: json.RawMessage(`{"productId":"premium"}`)}})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "untouched" {
		t.Errorf("symlink target was written through: %q", b)
	}
	fi, err := os.Lstat(filepath.Join(dir, "premium.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("premium.json is still a symlink; want a regular file")
	}
}

func TestRead_emptyStem_refuses(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".json", `{}`)
	if _, err := catalog.Read(dir); err == nil || !strings.Contains(err.Error(), "empty filename stem") {
		t.Fatalf("err = %v, want the empty-stem refusal", err)
	}
}

func TestWrite_refusesUnsafeProductID(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../evil", "a/b", `a\b`} {
		dir := t.TempDir()
		_, err := catalog.Write(dir, []catalog.Entry{{ProductID: id, Raw: json.RawMessage(`{}`)}})
		if err == nil || !strings.Contains(err.Error(), "unsafe productId") {
			t.Errorf("Write(%q) err = %v, want the unsafe-productId refusal", id, err)
		}
	}
}

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

func TestWrite_keepsHTMLCharactersLiteral(t *testing.T) {
	dir := t.TempDir()
	raw := json.RawMessage(`{"productId":"premium","listings":[{"title":"Tips & <tricks>"}]}`)
	if _, err := catalog.Write(dir, []catalog.Entry{{ProductID: "premium", Raw: raw}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "premium.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"listings\": [\n    {\n      \"title\": \"Tips & <tricks>\"\n    }\n  ],\n  \"productId\": \"premium\"\n}\n"
	if string(got) != want {
		t.Errorf("premium.json =\n%s\nwant:\n%s", got, want)
	}
}

func TestCount_matchesWhatAnEmptyWriteWouldRemove(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "premium.json", `{"productId":"premium"}`)
	write(t, dir, "draft.json", `{not json`)
	write(t, dir, ".json", `{}`)
	write(t, dir, "README.md", `not a catalog file`)
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "dangling.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	n := catalog.Count(dir)
	removed, err := catalog.Write(dir, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 4 || len(removed) != n {
		t.Errorf("Count = %d, Write removed %v: the guard must count every file a pull would erase", n, removed)
	}
	if got := catalog.Count(filepath.Join(dir, "absent")); got != 0 {
		t.Errorf("Count(missing dir) = %d, want 0", got)
	}
}
