package resolver

import (
	"context"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

const (
	EnvServiceAccount = "GPLAY_SERVICE_ACCOUNT"
	EnvAccount        = "GPLAY_ACCOUNT"
)

type noSourceError struct{}

func (noSourceError) Error() string {
	return "resolver: no credential source; pass --service-account, set GPLAY_SERVICE_ACCOUNT, " +
		"or run `gplay auth login`"
}

func (noSourceError) ExitCode() int { return 10 }

var ErrNoSource error = noSourceError{}

type Deps struct {
	Resolved *config.Resolved
	Keystore keystore.Backend
}

type Inputs struct {
	ServiceAccountFlag string
	AccountFlag        string
	EnvServiceAccount  string
	EnvAccount         string
}

func Resolve(ctx context.Context, deps Deps, in Inputs) (*serviceaccount.ServiceAccount, error) {
	sa, _, err := ResolveWithName(ctx, deps, in)
	return sa, err
}

func ResolveWithName(ctx context.Context, deps Deps, in Inputs) (*serviceaccount.ServiceAccount, string, error) {
	if in.ServiceAccountFlag != "" {
		sa, err := loadServiceAccount("--service-account", in.ServiceAccountFlag)
		return sa, "", err
	}

	if in.AccountFlag != "" {
		sa, err := loadStoredAccount(ctx, deps.Keystore, in.AccountFlag)
		if err != nil {
			return nil, "", err
		}
		return sa, in.AccountFlag, nil
	}

	if in.EnvServiceAccount != "" {
		sa, err := loadServiceAccount(EnvServiceAccount, in.EnvServiceAccount)
		return sa, "", err
	}

	if in.EnvAccount != "" {
		sa, err := loadStoredAccount(ctx, deps.Keystore, in.EnvAccount)
		if err != nil {
			return nil, "", err
		}
		return sa, in.EnvAccount, nil
	}

	if deps.Resolved == nil || deps.Resolved.ConfigAccount == "" {
		return nil, "", ErrNoSource
	}
	sa, err := loadStoredAccount(ctx, deps.Keystore, deps.Resolved.ConfigAccount)
	if err != nil {
		return nil, "", err
	}
	return sa, deps.Resolved.ConfigAccount, nil
}

func ResolveName(deps Deps, in Inputs) string {
	switch {
	case in.ServiceAccountFlag != "":
		return ""
	case in.AccountFlag != "":
		return in.AccountFlag
	case in.EnvServiceAccount != "":
		return ""
	case in.EnvAccount != "":
		return in.EnvAccount
	case deps.Resolved != nil:
		return deps.Resolved.ConfigAccount
	default:
		return ""
	}
}

func loadStoredAccount(ctx context.Context, ks keystore.Backend, name string) (*serviceaccount.ServiceAccount, error) {
	data, err := ks.Load(ctx, name)
	if err != nil {
		return nil, err
	}
	return serviceaccount.Parse(data)
}

func loadServiceAccount(source, value string) (*serviceaccount.ServiceAccount, error) {
	return serviceaccount.LoadValue(config.OSFS{}, source, value)
}
