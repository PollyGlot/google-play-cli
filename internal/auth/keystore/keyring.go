package keystore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/zalando/go-keyring"
)

// KeyringService is the service name used for every gplay credential stored
// in the OS keystore. It doubles as a namespace so a user can host other
// secrets in the same Keychain / Credential Manager / Secret Service ring
// without collisions.
const KeyringService = "gplay"

// reservedIndexUser is the keyring "user" field under which the keyring
// backend persists its own list of stored Account names. go-keyring exposes
// Set/Get/Delete keyed by (service, user) — there is no enumeration API —
// so the backend keeps its own index. The leading and trailing underscores
// mirror Python dunders to signal "do not poke at this from outside".
const reservedIndexUser = "__gplay_index__"

// probeUser is the keyring "user" field used by Select to probe whether the
// keyring is reachable. The probe value is written and immediately deleted.
const probeUser = "__gplay_probe__"

// ErrKeyringNotFound is the sentinel exposed for the test double in this
// package to mirror go-keyring's keyring.ErrNotFound. Production code never
// touches it — the keyring backend translates both into the package-level
// ErrNotFound.
var ErrKeyringNotFound = keyring.ErrNotFound

// KeyringAPI is the slice of the go-keyring surface the keyring backend
// depends on. Production wires the real package functions through
// realKeyring; tests inject a fake to keep the OS keystore out of unit runs.
type KeyringAPI interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

// realKeyring is the production adapter that forwards to the real
// go-keyring package functions.
type realKeyring struct{}

// DefaultKeyring returns the production KeyringAPI implementation. Use it
// when calling Select from non-test code.
func DefaultKeyring() KeyringAPI {
	return realKeyring{}
}

func (realKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (realKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (realKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// KeyringBackend stores credentials in the OS keystore via go-keyring. It
// satisfies the Backend interface.
type KeyringBackend struct {
	api     KeyringAPI
	service string
}

// NewKeyringBackend constructs a backend rooted at the given service name.
// Use KeyringService for the production value.
func NewKeyringBackend(api KeyringAPI, service string) *KeyringBackend {
	return &KeyringBackend{api: api, service: service}
}

// Save writes data under the given Account name and updates the index.
func (b *KeyringBackend) Save(name string, data []byte) error {
	if err := b.api.Set(b.service, name, string(data)); err != nil {
		return err
	}
	return b.addToIndex(name)
}

// Load returns the bytes stored under name, or ErrNotFound.
func (b *KeyringBackend) Load(name string) ([]byte, error) {
	v, err := b.api.Get(b.service, name)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return []byte(v), nil
}

// Delete removes the credential and updates the index. Returns ErrNotFound
// if absent.
func (b *KeyringBackend) Delete(name string) error {
	err := b.api.Delete(b.service, name)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return b.removeFromIndex(name)
}

// List returns the Account names currently stored.
func (b *KeyringBackend) List() ([]string, error) {
	return b.readIndex()
}

func (b *KeyringBackend) readIndex() ([]string, error) {
	v, err := b.api.Get(b.service, reservedIndexUser)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	if err := json.Unmarshal([]byte(v), &names); err != nil {
		return nil, err
	}
	return names, nil
}

func (b *KeyringBackend) writeIndex(names []string) error {
	data, err := json.Marshal(names)
	if err != nil {
		return err
	}
	return b.api.Set(b.service, reservedIndexUser, string(data))
}

func (b *KeyringBackend) addToIndex(name string) error {
	names, err := b.readIndex()
	if err != nil {
		return err
	}
	for _, n := range names {
		if n == name {
			return nil
		}
	}
	names = append(names, name)
	sort.Strings(names)
	return b.writeIndex(names)
}

func (b *KeyringBackend) removeFromIndex(name string) error {
	names, err := b.readIndex()
	if err != nil {
		return err
	}
	out := names[:0]
	for _, n := range names {
		if n != name {
			out = append(out, n)
		}
	}
	if len(out) == len(names) {
		return nil
	}
	return b.writeIndex(out)
}

// SelectOptions configures Select. Both fields are required.
type SelectOptions struct {
	// Keyring is the go-keyring slice the keyring backend will talk to.
	// Production callers should pass DefaultKeyring(); tests pass a fake.
	Keyring KeyringAPI
	// FileRoot is the directory the file backend will use when the keyring
	// is unavailable.
	FileRoot string
}

// selectResult caches the (Backend, label) pair returned by the first
// Select call so subsequent calls reuse it. The Backend label is what
// `auth status` displays and what the -v log line reports.
type selectResult struct {
	backend Backend
	label   string
}

var (
	selectOnce sync.Once
	selectVal  selectResult
)

// Select picks the credential backend for this process. It probes the
// keyring with a Set+Delete round-trip; on any error it falls back to the
// file backend. The result is cached for the lifetime of the process.
//
// The returned label is "keyring" or "file" — it is what `auth status`
// displays and what the -v log line reports. The label is invariant once
// chosen, so logging it once per process is safe.
func Select(opts SelectOptions) (Backend, string, error) {
	selectOnce.Do(func() {
		if probeKeyring(opts.Keyring) {
			selectVal = selectResult{
				backend: NewKeyringBackend(opts.Keyring, KeyringService),
				label:   "keyring",
			}
			return
		}
		selectVal = selectResult{
			backend: NewFileBackend(opts.FileRoot),
			label:   "file",
		}
	})
	return selectVal.backend, selectVal.label, nil
}

// probeKeyring returns true if the keyring accepts a write+delete round
// trip. Any error (including keyring.ErrUnsupportedPlatform on a build
// without a native provider, or a dbus failure on headless Linux) causes
// the probe to fail and the caller falls back to the file backend.
func probeKeyring(api KeyringAPI) bool {
	if api == nil {
		return false
	}
	if err := api.Set(KeyringService, probeUser, "ok"); err != nil {
		return false
	}
	// Best-effort cleanup; the probe value is tiny and namespaced, so we
	// don't fail the selection if Delete errors here.
	_ = api.Delete(KeyringService, probeUser)
	return true
}

// ResetSelectForTest clears the cached selection. Test-only helper kept
// unexported (`_test.go`-suffixed name) where possible — but Go forbids
// _test.go content from being referenced across packages without an
// export, and the cmd-level test must reset between cases. Keeping it
// in production code is the smallest seam.
func ResetSelectForTest() {
	selectOnce = sync.Once{}
	selectVal = selectResult{}
	logOnce = sync.Once{}
}

var logOnce sync.Once

// LogBackendOnce writes "keystore: using <label> backend\n" to w exactly
// once per process. The selector caches its result, so this label is
// invariant for the life of the process — emitting it once is enough to
// tell a debugger where credentials live, and repeat lines would be
// noise in long-running test/CI runs.
func LogBackendOnce(w io.Writer, label string) {
	logOnce.Do(func() {
		_, _ = fmt.Fprintf(w, "keystore: using %s backend\n", label)
	})
}
