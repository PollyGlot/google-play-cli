package keystore_test

import (
	"bytes"
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

	if err := be.Save("ci", want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := be.Load("ci")
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

	if err := be.Save("ci", []byte("{}")); err != nil {
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
	_, err := be.Load("nope")
	if !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("Load: got %v, want ErrNotFound", err)
	}
}

func TestFileBackend_delete_removesEntry(t *testing.T) {
	be, _ := newFileBackend(t)
	if err := be.Save("ci", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := be.Delete("ci"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := be.Load("ci"); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("after Delete: Load got %v, want ErrNotFound", err)
	}
	if err := be.Delete("ci"); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("double Delete: got %v, want ErrNotFound", err)
	}
}

func TestFileBackend_list_returnsSavedNamesWithoutSuffix(t *testing.T) {
	be, _ := newFileBackend(t)
	for _, n := range []string{"alpha", "beta", "gamma"} {
		if err := be.Save(n, []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	names, err := be.List()
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
	if err := be.Save("ci", []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := be.Save("ci", []byte(`{"v":2}`)); err != nil {
		t.Fatalf("Save (overwrite): %v", err)
	}
	got, err := be.Load("ci")
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

	if err := be.Save("ci", []byte("{}")); err != nil {
		t.Fatalf("Save into uncreated dir: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("Save did not create parent dir: %v", err)
	}
}
