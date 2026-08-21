// Package registry is the pure-data registry of Android package names
// known to each gplay Account. It operates on a []config.Account slice
// shared with config.Global / config.Resolved; persistence (config.Save)
// is the caller's job. No IO, no logging, no global state: every
// behavior is exercised through table-driven tests against an in-memory
// slice.
package registry

import (
	"fmt"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

// unknownAccountError is the typed error returned when an operation
// names an account that is not in the registry. It carries ExitCode 2
// so `gplay apps add --account foo <pkg>` exits as CLI misuse rather
// than the generic 1. Is reports true against the ErrUnknownAccount
// sentinel so callers can branch on the failure mode regardless of
// which name triggered it.
type unknownAccountError struct{ name string }

func (e unknownAccountError) Error() string {
	if e.name == "" {
		return "registry: unknown account"
	}
	return fmt.Sprintf("registry: unknown account %q", e.name)
}
func (unknownAccountError) ExitCode() int { return 2 }
func (unknownAccountError) Is(target error) bool {
	_, ok := target.(unknownAccountError)
	return ok
}

// ErrUnknownAccount is the sentinel returned by Add when the named
// account does not exist in the registry.
var ErrUnknownAccount error = unknownAccountError{}

// Add records pkg under the named account. It is idempotent: a second
// Add with the same (account, pkg) pair leaves the registry unchanged.
// Returns an error wrapping ErrUnknownAccount when account is not in
// the registry. The slice is mutated in place; the caller persists the
// result via config.Global.Save.
func Add(accounts *[]config.Account, account, pkg string) error {
	for i := range *accounts {
		if (*accounts)[i].Name != account {
			continue
		}
		for _, p := range (*accounts)[i].Packages {
			if p == pkg {
				return nil
			}
		}
		(*accounts)[i].Packages = append((*accounts)[i].Packages, pkg)
		return nil
	}
	return unknownAccountError{name: account}
}

// Has reports whether pkg is registered under account.
func Has(accounts []config.Account, account, pkg string) bool {
	for _, a := range accounts {
		if a.Name != account {
			continue
		}
		for _, p := range a.Packages {
			if p == pkg {
				return true
			}
		}
		return false
	}
	return false
}

// Remove drops pkg from account's Packages slice. Calling Remove on a
// (account, pkg) pair that is not registered is a no-op: the intent is
// "this should not be in the registry", which is already true. An
// unknown account is also a no-op for the same reason: there is no
// state to undo.
//
// The new Packages slice is built into fresh storage rather than via
// `append(pkgs[:j], pkgs[j+1:]...)` so any caller that previously held
// a slice from List still sees its original contents: `append` over
// the same backing array would otherwise rewrite the element at index
// j with the element from j+1, leaving the caller's snapshot with a
// duplicated entry where pkg used to be.
func Remove(accounts *[]config.Account, account, pkg string) error {
	for i := range *accounts {
		if (*accounts)[i].Name != account {
			continue
		}
		pkgs := (*accounts)[i].Packages
		for j, p := range pkgs {
			if p != pkg {
				continue
			}
			next := make([]string, 0, len(pkgs)-1)
			next = append(next, pkgs[:j]...)
			next = append(next, pkgs[j+1:]...)
			(*accounts)[i].Packages = next
			return nil
		}
		return nil
	}
	return nil
}

// List returns a defensive copy of the Packages slice registered under
// account, or nil if the account does not exist. The copy isolates the
// caller from in-place mutations the registry performs internally (or
// vice-versa: a caller's sort/filter does not bleed back into storage).
// Returning the underlying slice would be cheaper but leaks the
// registry's representation, and a caller's sort.Strings or append is
// then a silent corruption of on-disk insertion order.
func List(accounts []config.Account, account string) []string {
	for _, a := range accounts {
		if a.Name != account {
			continue
		}
		if a.Packages == nil {
			return nil
		}
		out := make([]string, len(a.Packages))
		copy(out, a.Packages)
		return out
	}
	return nil
}
