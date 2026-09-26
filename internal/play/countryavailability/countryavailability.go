// Package countryavailability reads the Country availability of a track
// via edits.countryavailability.get, inside an Edit the caller has
// already opened. The resource is READ-ONLY (the Developer API exposes no
// insert/patch/update) and keyed by TRACK, not by app: see ADR-0012.
// gplay surfaces it at that real grain (per track) and at that real
// capability (read-only); a user who wants to change availability is
// pointed at the Play Console.
package countryavailability

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opCountryAvailabilityGet = "countryavailability.get"

// method is the registry entry this package calls. Resolving it at init makes
// an unregistered or vanished method a startup panic in CI (the registry tests
// resolve every entry), never a runtime surprise for a user; the verb and the
// URL template then come from the Discovery snapshot instead of a literal
// maintained here (#513).
var method = apiregistry.MustResolve("androidpublisher.edits.countryavailability.get")

// TargetedCountry is one element of the countries[] array: a CLDR
// two-letter country code. json tag mirrors the API verbatim (ADR-0003
// pass-through).
type TargetedCountry struct {
	CountryCode string `json:"countryCode"`
}

// TrackCountryAvailability is the API-shaped edits.countryavailability
// resource for a single track: whether availability syncs with the
// production track, whether the artifacts reach "rest of world"
// countries, and the explicit list of targeted countries. json tags
// mirror the API verbatim (ADR-0003 pass-through).
type TrackCountryAvailability struct {
	SyncWithProduction bool              `json:"syncWithProduction"`
	RestOfWorld        bool              `json:"restOfWorld"`
	Countries          []TargetedCountry `json:"countries"`
}

// Get fetches the Country availability of track at
// edits.countryavailability.get. The URL path segment is camelCase
// (countryAvailability) even though the resource is spelled
// countryavailability. Returns the parsed *TrackCountryAvailability and
// the raw JSON body for the --output json pass-through (ADR-0003) and for
// diagnostics. Like tracks.Get / testers.Get, it runs inside an Edit the
// caller has already opened; errors propagate as *api.Error so the
// gplay exit-code taxonomy maps transparently (403 → 11, 404 → 30,
// 5xx → 40, network → 50).
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
		// A body that does not decode keeps the 200 status tag it always
		// had, so its exit code (30) is unchanged.
		return nil, raw, &api.Error{
			Operation:  opCountryAvailabilityGet,
			Package:    pkg,
			StatusCode: http.StatusOK,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return &parsed, raw, nil
}
