package details

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

const (
	opDetailsGet   = "details.get"
	opDetailsPatch = "details.patch"
	opListingsGet  = "listings.get"
)

var (
	methodDetailsGet   = apiregistry.MustResolve("androidpublisher.edits.details.get")
	methodDetailsPatch = apiregistry.MustResolve("androidpublisher.edits.details.patch")
	methodListingsGet  = apiregistry.MustResolve("androidpublisher.edits.listings.get")
)

type Details struct {
	DefaultLanguage string `json:"defaultLanguage"`
	Title           string `json:"title"`
	ContactEmail    string `json:"contactEmail"`
	Icon            *Icon  `json:"-"`
}

type Icon struct {
	URL    string `json:"url"`
	Sha256 string `json:"sha256"`
}

type AppDetails struct {
	DefaultLanguage string `json:"defaultLanguage"`
	ContactEmail    string `json:"contactEmail"`
	ContactPhone    string `json:"contactPhone"`
	ContactWebsite  string `json:"contactWebsite"`
}

func GetDetails(ctx context.Context, hc *http.Client, pkg string) (*AppDetails, json.RawMessage, error) {
	var (
		out *AppDetails
		raw json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(ctx, hc, pkg, func(editID string) error {
		body, status, err := getJSON(ctx, hc, methodDetailsGet, opDetailsGet, pkg, editParams(pkg, editID))
		if err != nil {
			return err
		}
		var parsed AppDetails
		if err := json.Unmarshal(body, &parsed); err != nil {
			return api.DecodeFailure(api.Error{
				Operation:  opDetailsGet,
				Package:    pkg,
				StatusCode: status,
				Message:    "decode response: " + err.Error(),
				Cause:      err,
			})
		}
		out, raw = &parsed, body
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return out, raw, nil
}

func Get(ctx context.Context, hc *http.Client, pkg string) (*Details, json.RawMessage, error) {
	var (
		out *Details
		raw json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(ctx, hc, pkg, func(editID string) error {
		detailsRaw, defaultLang, contactEmail, err := fetchDetails(ctx, hc, pkg, editID)
		if err != nil {
			return err
		}
		listingRaw, title, err := fetchListing(ctx, hc, pkg, editID, defaultLang)
		if err != nil {
			return err
		}
		icon, err := fetchIcon(ctx, hc, pkg, editID, defaultLang)
		if err != nil {
			return err
		}
		envelope, err := output.Marshal(struct {
			Details json.RawMessage `json:"details"`
			Listing json.RawMessage `json:"listing"`
			Icon    *Icon           `json:"icon,omitempty"`
		}{
			Details: detailsRaw,
			Listing: listingRaw,
			Icon:    icon,
		})
		if err != nil {
			return &api.Error{Operation: "details.envelope", Package: pkg, Message: "marshal envelope: " + err.Error(), Cause: err}
		}
		out = &Details{
			DefaultLanguage: defaultLang,
			Title:           title,
			ContactEmail:    contactEmail,
			Icon:            icon,
		}
		raw = envelope
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return out, raw, nil
}

type AppDetailsPatch struct {
	DefaultLanguage *string `json:"defaultLanguage,omitempty"`
	ContactEmail    *string `json:"contactEmail,omitempty"`
	ContactPhone    *string `json:"contactPhone,omitempty"`
	ContactWebsite  *string `json:"contactWebsite,omitempty"`
}

func Patch(ctx context.Context, hc *http.Client, pkg, editID string, patch AppDetailsPatch) (*AppDetails, json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodDetailsPatch, Op: opDetailsPatch, Target: pkg,
		Params: editParams(pkg, editID),
		Body:   patch,
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed AppDetails
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, api.DecodeFailure(api.Error{
			Operation: opDetailsPatch,
			Package:   pkg,
			Message:   "decode response: " + err.Error(),
			Cause:     err,
		})
	}
	return &parsed, raw, nil
}

func fetchDetails(ctx context.Context, hc *http.Client, pkg, editID string) (json.RawMessage, string, string, error) {
	raw, status, err := getJSON(ctx, hc, methodDetailsGet, opDetailsGet, pkg, editParams(pkg, editID))
	if err != nil {
		return nil, "", "", err
	}
	var parsed struct {
		DefaultLanguage string `json:"defaultLanguage"`
		ContactEmail    string `json:"contactEmail"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, "", "", api.DecodeFailure(api.Error{
			Operation:  opDetailsGet,
			Package:    pkg,
			StatusCode: status,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		})
	}
	if parsed.DefaultLanguage == "" {
		return nil, "", "", api.DecodeFailure(api.Error{
			Operation:  opDetailsGet,
			Package:    pkg,
			StatusCode: status,
			Message:    "missing defaultLanguage in response body",
		})
	}
	return raw, parsed.DefaultLanguage, parsed.ContactEmail, nil
}

func fetchListing(ctx context.Context, hc *http.Client, pkg, editID, language string) (json.RawMessage, string, error) {
	params := editParams(pkg, editID)
	params["language"] = language
	raw, status, err := getJSON(ctx, hc, methodListingsGet, opListingsGet, pkg, params)
	if err != nil {
		return nil, "", err
	}
	var parsed struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, "", api.DecodeFailure(api.Error{
			Operation:  opListingsGet,
			Package:    pkg,
			StatusCode: status,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		})
	}
	return raw, parsed.Title, nil
}

func fetchIcon(ctx context.Context, hc *http.Client, pkg, editID, language string) (*Icon, error) {
	imgs, _, err := images.List(ctx, hc, pkg, editID, language, images.Icon)
	if err != nil {
		return nil, err
	}
	if len(imgs) == 0 {
		return nil, nil
	}
	return &Icon{URL: imgs[0].URL, Sha256: imgs[0].Sha256}, nil
}

func getJSON(ctx context.Context, hc *http.Client, m apiregistry.Method, op, pkg string, params map[string]string) (json.RawMessage, int, error) {
	raw, err := api.Do(ctx, hc, api.Call{Method: m, Op: op, Target: pkg, Params: params})
	if err != nil {
		return nil, 0, err
	}
	return raw, http.StatusOK, nil
}

func editParams(pkg, editID string) map[string]string {
	return map[string]string{"packageName": pkg, "editId": editID}
}

func GetDefaultLanguage(ctx context.Context, hc *http.Client, pkg, editID string) (string, error) {
	const op = "edits.details.get"
	body, status, err := getJSON(ctx, hc, methodDetailsGet, op, pkg, editParams(pkg, editID))
	if err != nil {
		return "", err
	}
	var parsed struct {
		DefaultLanguage string `json:"defaultLanguage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", api.DecodeFailure(api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: status,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		})
	}
	if parsed.DefaultLanguage == "" {
		return "", api.DecodeFailure(api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: status,
			Message:    "missing defaultLanguage in response body",
		})
	}
	return parsed.DefaultLanguage, nil
}
