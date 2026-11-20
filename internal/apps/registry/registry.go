package registry

import (
	"fmt"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

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

var ErrUnknownAccount error = unknownAccountError{}

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
