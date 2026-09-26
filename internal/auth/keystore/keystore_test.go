package keystore_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
)

func newFileBackend(t *testing.T) (keystore.Backend, string) {
	t.Helper()
	dir := t.TempDir()
	return keystore.NewFileBackend(dir), dir
}

func TestFileBackend_saveLoad_roundTrips(t *testing.T) {
	be, _ := newFileBackend(t)
	want := []byte(`{"client_email":"x@y.iam"}`)

	if err := be.Save(context.Background(), "ci", want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := be.Load(context.Background(), "ci")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Load returned %q, want %q", got, want)
	}
}

func TestFileBackend_save_writesMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes do not apply on Windows")
	}
	be, root := newFileBackend(t)

	if err := be.Save(context.Background(), "ci", []byte("{}")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "ci.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %o, want 0600", got)
	}
}

func TestFileBackend_load_missing_returnsErrNotFound(t *testing.T) {
	be, _ := newFileBackend(t)
	_, err := be.Load(context.Background(), "nope")
	if !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("Load: got %v, want ErrNotFound", err)
	}
}

func TestFileBackend_delete_removesEntry(t *testing.T) {
	be, _ := newFileBackend(t)
	if err := be.Save(context.Background(), "ci", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := be.Delete(context.Background(), "ci"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := be.Load(context.Background(), "ci"); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("after Delete: Load got %v, want ErrNotFound", err)
	}
	if err := be.Delete(context.Background(), "ci"); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("double Delete: got %v, want ErrNotFound", err)
	}
}

func TestFileBackend_list_returnsSavedNamesWithoutSuffix(t *testing.T) {
	be, _ := newFileBackend(t)
	for _, n := range []string{"alpha", "beta", "gamma"} {
		if err := be.Save(context.Background(), n, []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	names, err := be.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(names)
	want := []string{"alpha", "beta", "gamma"}
	if len(names) != len(want) {
		t.Fatalf("List returned %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("names[%d] = %q, want %q", i, names[i], n)
		}
	}
}

func TestFileBackend_save_overwritesExisting(t *testing.T) {
	be, _ := newFileBackend(t)
	if err := be.Save(context.Background(), "ci", []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := be.Save(context.Background(), "ci", []byte(`{"v":2}`)); err != nil {
		t.Fatalf("Save (overwrite): %v", err)
	}
	got, err := be.Load(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"v":2}` {
		t.Errorf("Load after overwrite = %q, want %q", got, `{"v":2}`)
	}
}

func TestFileBackend_save_createsParentDirIfMissing(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "deep", "not", "yet", "made")
	be := keystore.NewFileBackend(nested)

	if err := be.Save(context.Background(), "ci", []byte("{}")); err != nil {
		t.Fatalf("Save into uncreated dir: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("Save did not create parent dir: %v", err)
	}
}

// TestFileBackend_pathLikeName_refusedWithoutTouchingDisk asserts the file
// backend refuses an Account name that is not one plain path component. The
// name arrives from --name, --account, GPLAY_ACCOUNT and a repo's
// config.local.json; joined as-is, `../x` reads, writes or deletes x.json one
// directory above the keystore (#603).
func TestFileBackend_pathLikeName_refusedWithoutTouchingDisk(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "accounts")
	be := keystore.NewFileBackend(root)
	outside := filepath.Join(parent, "victim.json")
	const victim = `{"client_email":"other@tenant"}`
	if err := os.WriteFile(outside, []byte(victim), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const name = "../victim"

	if err := be.Save(ctx, name, []byte(`{}`)); err == nil {
		t.Error("Save accepted a path-like name")
	}
	if data, err := be.Load(ctx, name); err == nil {
		t.Errorf("Load read %q through a path-like name", data)
	} else if errors.Is(err, keystore.ErrNotFound) {
		t.Error("Load returned ErrNotFound, want a refusal: not-found lets the resolver fall through silently")
	}
	// Delete answers "nothing stored here": Save never stores such a name, and
	// logout sweeps the file backend after the keyring, so a refusal would
	// fail the logout of a keyring Account whose name has a slash.
	if err := be.Delete(ctx, name); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("Delete(%q) = %v, want ErrNotFound", name, err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != victim {
		t.Errorf("file outside the keystore changed: %q, %v", got, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Save created the keystore directory before refusing: %v", err)
	}
}
