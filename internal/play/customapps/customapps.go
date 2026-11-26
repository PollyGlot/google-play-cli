package customapps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const op = "customApps.create"

var method = apiregistry.MustResolve("playcustomapp.accounts.customApps.create")

type Organization struct {
	OrganizationID   string `json:"organizationId,omitempty"`
	OrganizationName string `json:"organizationName,omitempty"`
}

type CustomApp struct {
	Title         string         `json:"title,omitempty"`
	LanguageCode  string         `json:"languageCode,omitempty"`
	PackageName   string         `json:"packageName,omitempty"`
	Organizations []Organization `json:"organizations,omitempty"`
}

type CreateOpts struct {
	Title         string
	LanguageCode  string
	Organizations []Organization
}

type LocalIOError struct {
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: %s: %v", op, e.Path, e.Cause)
}

func (e *LocalIOError) Unwrap() error { return e.Cause }

func (e *LocalIOError) ExitCode() int { return 20 }

func Create(ctx context.Context, hc *http.Client, account, artifactPath string, opts CreateOpts) (CustomApp, json.RawMessage, error) {
	metaJSON, err := json.Marshal(CustomApp{
		Title:         opts.Title,
		LanguageCode:  opts.LanguageCode,
		Organizations: opts.Organizations,
	})
	if err != nil {
		return CustomApp{}, nil, api.BuildFailure(api.Error{Operation: op, Resource: developerAccount(account), Message: "marshal metadata: " + err.Error(), Cause: err})
	}

	f, err := os.Open(artifactPath)
	if err != nil {
		return CustomApp{}, nil, &LocalIOError{Path: artifactPath, Cause: err}
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return CustomApp{}, nil, &LocalIOError{Path: artifactPath, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return CustomApp{}, nil, &LocalIOError{Path: artifactPath, Cause: fmt.Errorf("not a regular file")}
	}

	u, err := method.UploadURL(map[string]string{"account": account})
	if err != nil {
		return CustomApp{}, nil, api.BuildFailure(api.Error{Operation: op, Resource: developerAccount(account), Message: err.Error(), Cause: err})
	}

	raw, status, err := api.ResumableUploadWithInitiateBody(
		ctx, hc, op, developerAccount(account), u, "application/octet-stream", f, info.Size(),
		metaJSON, "application/json; charset=UTF-8", nil,
	)
	if err != nil {
		return CustomApp{}, nil, err
	}

	var parsed CustomApp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return CustomApp{}, nil, api.DecodeFailure(api.Error{Operation: op, Resource: developerAccount(account), StatusCode: status, Message: "decode response: " + err.Error(), Cause: err})
	}
	return parsed, raw, nil
}

func developerAccount(account string) api.Resource {
	return api.Resource{Kind: api.KindDeveloperAccount, ID: account}
}
