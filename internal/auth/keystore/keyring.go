package keystore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/zalando/go-keyring"
)

const KeyringService = "gplay"

const (
	BackendKeyring = "keyring"
	BackendFile    = "file"
)

const reservedIndexUser = "__gplay_index__"

const probeUser = "__gplay_probe__"

type KeyringAPI interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

type realKeyring struct{}

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

type KeyringBackend struct {
	api     KeyringAPI
	service string
}

func NewKeyringBackend(api KeyringAPI, service string) *KeyringBackend {
	return &KeyringBackend{api: api, service: service}
}

func (b *KeyringBackend) Save(_ context.Context, name string, data []byte) error {
	if err := b.api.Set(b.service, name, string(data)); err != nil {
		return err
	}
	return b.addToIndex(name)
}

func (b *KeyringBackend) Load(_ context.Context, name string) ([]byte, error) {
	v, err := b.api.Get(b.service, name)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return []byte(v), nil
}

func (b *KeyringBackend) Delete(_ context.Context, name string) error {
	err := b.api.Delete(b.service, name)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return b.removeFromIndex(name)
}

func (b *KeyringBackend) List(_ context.Context) ([]string, error) {
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

type SelectOptions struct {
	Keyring  KeyringAPI
	FileRoot string
}

func Select(_ context.Context, opts SelectOptions) (Backend, string, error) {
	if probeKeyring(opts.Keyring) {
		return NewKeyringBackend(opts.Keyring, KeyringService), BackendKeyring, nil
	}
	return NewFileBackend(opts.FileRoot), BackendFile, nil
}

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

func LogBackend(w io.Writer, label string) {
	_, _ = fmt.Fprintf(w, "keystore: using %s backend\n", label)
}
