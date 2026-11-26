package listings

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opListingsList   = "listings.list"
	opListingsGet    = "listings.get"
	opListingsPatch  = "listings.patch"
	opListingsUpdate = "listings.update"
	opListingsDelete = "listings.delete"
)

var (
	methodList   = apiregistry.MustResolve("androidpublisher.edits.listings.list")
	methodGet    = apiregistry.MustResolve("androidpublisher.edits.listings.get")
	methodPatch  = apiregistry.MustResolve("androidpublisher.edits.listings.patch")
	methodUpdate = apiregistry.MustResolve("androidpublisher.edits.listings.update")
	methodDelete = apiregistry.MustResolve("androidpublisher.edits.listings.delete")
)

type Listing struct {
	Language         string `json:"language"`
	Title            string `json:"title"`
	FullDescription  string `json:"fullDescription"`
	ShortDescription string `json:"shortDescription"`
	Video            string `json:"video"`
}

func List(ctx context.Context, hc *http.Client, pkg, editID string) ([]Listing, json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodList, Op: opListingsList, Target: pkg,
		Params: map[string]string{"packageName": pkg, "editId": editID},
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed struct {
		Listings []Listing `json:"listings"`
	}
	if err := decode(opListingsList, pkg, raw, &parsed); err != nil {
		return nil, raw, err
	}
	return parsed.Listings, raw, nil
}

func Get(ctx context.Context, hc *http.Client, pkg, editID, language string) (*Listing, json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodGet, Op: opListingsGet, Target: pkg,
		Params: localeParams(pkg, editID, language),
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed Listing
	if err := decode(opListingsGet, pkg, raw, &parsed); err != nil {
		return nil, raw, err
	}
	return &parsed, raw, nil
}

func Patch(ctx context.Context, hc *http.Client, pkg, editID, language string, body []byte) (json.RawMessage, error) {
	return write(ctx, hc, methodPatch, opListingsPatch, pkg, editID, language, body)
}

func Update(ctx context.Context, hc *http.Client, pkg, editID, language string, body []byte) (json.RawMessage, error) {
	return write(ctx, hc, methodUpdate, opListingsUpdate, pkg, editID, language, body)
}

func write(ctx context.Context, hc *http.Client, m apiregistry.Method, op, pkg, editID, language string, body []byte) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: m, Op: op, Target: pkg,
		Params: localeParams(pkg, editID, language),
		Body:   body,
	})
}

func Delete(ctx context.Context, hc *http.Client, pkg, editID, language string) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: methodDelete, Op: opListingsDelete, Target: pkg,
		Params: localeParams(pkg, editID, language),
	})
	return err
}

func localeParams(pkg, editID, language string) map[string]string {
	return map[string]string{"packageName": pkg, "editId": editID, "language": language}
}

func decode(op, pkg string, raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return api.DecodeFailure(api.Error{Operation: op, Package: pkg, Message: "decode response: " + err.Error(), Cause: err})
	}
	return nil
}
