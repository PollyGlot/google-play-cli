package keystore_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
)

// fakeKeyring is the test double for the go-keyring slice the keyring backend
// depends on. It tracks calls and lets each test simulate either a working
// keystore or an unavailable one via the configurable hooks.
type fakeKeyring struct {
	mu       sync.Mutex
	store    map[string]string // key = service + "\x00" + user
	setErr   error
	getErr   error
	delErr   error
	probeErr error
	probed   bool

	calls []string
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{store: map[string]string{}}
}

func (f *fakeKeyring) key(service, user string) string {
	return service + "\x00" + user
}

func (f *fakeKeyring) Set(service, user, pass string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Set:"+user)
	// The selector probes by Set("gplay", "__gplay_probe__", "ok"). Route the
	// probe through probeErr so individual tests can force availability.
	if user == "__gplay_probe__" {
		f.probed = true
		if f.probeErr != nil {
			return f.probeErr
		}
	}
	if f.setErr != nil {
		return f.setErr
	}
	f.store[f.key(service, user)] = pass
	return nil
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Get:"+user)
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.store[f.key(service, user)]
	if !ok {
		return "", keystore.ErrKeyringNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Delete:"+user)
	if f.delErr != nil {
		return f.delErr
	}
	if _, ok := f.store[f.key(service, user)]; !ok {
		return keystore.ErrKeyringNotFound
	}
	delete(f.store, f.key(service, user))
	return nil
}

func TestSelect_keyringAvailable_returnsKeyringBackend(t *testing.T) {
	keystore.ResetSelectForTest()
	fk := newFakeKeyring()
	dir := t.TempDir()

	be, label, err := keystore.Select(keystore.SelectOptions{
		Keyring:  fk,
		FileRoot: dir,
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if label != "keyring" {
		t.Errorf("label = %q, want %q", label, "keyring")
	}
	if !fk.probed {
		t.Errorf("Select did not probe the keyring")
	}

	// Sanity: Save flows through the keyring, not the filesystem.
	if err := be.Save("alpha", []byte(`{"v":1}`)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := be.Load("alpha")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(got, []byte(`{"v":1}`)) {
		t.Errorf("Load returned %q, want %q", got, `{"v":1}`)
	}
	// And nothing under the file root.
	if entries, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(entries) != 0 {
		t.Errorf("keyring backend created file artifacts: %v", entries)
	}
}

func TestSelect_keyringUnavailable_fallsBackToFile(t *testing.T) {
	keystore.ResetSelectForTest()
	fk := newFakeKeyring()
	fk.probeErr = errors.New("Secret Service unavailable")
	dir := t.TempDir()

	be, label, err := keystore.Select(keystore.SelectOptions{
		Keyring:  fk,
		FileRoot: dir,
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if label != "file" {
		t.Errorf("label = %q, want %q", label, "file")
	}

	if err := be.Save("alpha", []byte(`{"v":1}`)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// File backend writes <dir>/alpha.json.
	if _, err := keystore.NewFileBackend(dir).Load("alpha"); err != nil {
		t.Errorf("expected file backend persistence after fallback, got %v", err)
	}
}

func TestKeyringBackend_saveLoad_roundTrips(t *testing.T) {
	fk := newFakeKeyring()
	be := keystore.NewKeyringBackend(fk, "gplay")

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

func TestKeyringBackend_load_missing_returnsErrNotFound(t *testing.T) {
	fk := newFakeKeyring()
	be := keystore.NewKeyringBackend(fk, "gplay")

	_, err := be.Load("nope")
	if !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("Load: got %v, want ErrNotFound", err)
	}
}

func TestKeyringBackend_delete_removesEntry(t *testing.T) {
	fk := newFakeKeyring()
	be := keystore.NewKeyringBackend(fk, "gplay")

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

func TestKeyringBackend_list_returnsSavedNamesAndExcludesReservedIndex(t *testing.T) {
	fk := newFakeKeyring()
	be := keystore.NewKeyringBackend(fk, "gplay")

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

func TestSelect_isCachedAcrossCalls(t *testing.T) {
	keystore.ResetSelectForTest()
	fk := newFakeKeyring()
	dir := t.TempDir()

	opts := keystore.SelectOptions{Keyring: fk, FileRoot: dir}
	be1, label1, err := keystore.Select(opts)
	if err != nil {
		t.Fatalf("first Select: %v", err)
	}
	// Reset the probe flag — if we hit the keyring again, the test fails.
	fk.mu.Lock()
	fk.probed = false
	fk.mu.Unlock()

	be2, label2, err := keystore.Select(opts)
	if err != nil {
		t.Fatalf("second Select: %v", err)
	}
	if label1 != label2 {
		t.Errorf("second Select returned label %q, want %q", label2, label1)
	}
	if be1 != be2 {
		t.Errorf("second Select returned a different backend instance")
	}
	if fk.probed {
		t.Errorf("Select probed the keyring on the second call; result is not cached")
	}
}
