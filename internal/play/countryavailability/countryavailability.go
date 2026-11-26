package countryavailability

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opCountryAvailabilityGet = "countryavailability.get"

var method = apiregistry.MustResolve("androidpublisher.edits.countryavailability.get")

type TargetedCountry struct {
	CountryCode string `json:"countryCode"`
}

type TrackCountryAvailability struct {
	SyncWithProduction bool              `json:"syncWithProduction"`
	RestOfWorld        bool              `json:"restOfWorld"`
	Countries          []TargetedCountry `json:"countries"`
}

func Get(ctx context.Context, hc *http.Client, pkg, editID, track string) (*TrackCountryAvailability, json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: method, Op: opCountryAvailabilityGet, Target: pkg,
		Params: map[string]string{"packageName": pkg, "editId": editID, "track": track},
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed TrackCountryAvailability
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, api.DecodeFailure(api.Error{
			Operation: opCountryAvailabilityGet,
			Package:   pkg,
			Message:   "decode response: " + err.Error(),
			Cause:     err,
		})
	}
	return &parsed, raw, nil
}
