package keystore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/zalando/go-keyring"
)

// KeyringService is the service name used for every gplay credential stored
// in the OS keystore. It doubles as a namespace so a user can host other
// secrets in the same Keychain / Credential Manager / Secret Service ring
// without collisions.
const KeyringService = "gplay"

// Backend labels returned by Select. Stable identifiers for the JSON
// output of `gplay auth status` and for the -v `keystore: using <label>
// backend` log line.
const (
	BackendKeyring = "keyring"
	BackendFile    = "file"
)

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
	if slices.Contains(names, name) {
		return nil
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
	origLen := len(names)
	out := slices.DeleteFunc(names, func(n string) bool { return n == name })
	if len(out) == origLen {
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

// Select picks the credential backend for this process. It probes the
// keyring with a Set+Delete round-trip; on any error it falls back to
// the file backend. The probe is cheap (one tiny namespaced key); call
// once per invocation from the kernel and pass the result down. No
// process-level caching is involved — each call is independent.
//
// The returned label is "keyring" or "file" — it is what `auth status`
// displays and what the -v log line reports. The label is invariant
// for a given (Keyring, FileRoot) pair; the kernel logs it once per
// RunContext.
func Select(opts SelectOptions) (Backend, string, error) {
	if probeKeyring(opts.Keyring) {
		return NewKeyringBackend(opts.Keyring, KeyringService), BackendKeyring, nil
	}
	return NewFileBackend(opts.FileRoot), BackendFile, nil
}

// probeKeyring returns true if the keyring accepts a full write+delete
// round trip. Either step failing (Set or Delete) demotes us to the
// file backend — if Delete is broken, a later `gplay auth logout` would
// fail too, and we want that surfaced at selection time rather than
// halfway through a credential cleanup.
func probeKeyring(api KeyringAPI) bool {
	if api == nil {
		return false
	}
	if err := api.Set(KeyringService, probeUser, "ok"); err != nil {
		return false
	}
	if err := api.Delete(KeyringService, probeUser); err != nil {
		return false
	}
	return true
}

// LogBackend writes "keystore: using <label> backend\n" to w. The
// caller owns the once-per-invocation semantics (see
// kernel.RunContext.Backend) — this function is a small renderer with
// no globals.
func LogBackend(w io.Writer, label string) {
	_, _ = fmt.Fprintf(w, "keystore: using %s backend\n", label)
}
