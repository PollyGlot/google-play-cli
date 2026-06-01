// Package countryavailability reads the Country availability of a track
// via edits.countryavailability.get, inside an Edit the caller has
// already opened. The resource is READ-ONLY (the Developer API exposes no
// insert/patch/update) and keyed by TRACK, not by app — see ADR-0012.
// gplay surfaces it at that real grain (per track) and at that real
// capability (read-only); a user who wants to change availability is
// pointed at the Play Console.
package countryavailability

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opCountryAvailabilityGet = "countryavailability.get"

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
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/countryAvailability/" + url.PathEscape(track)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, &api.Error{Operation: opCountryAvailabilityGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &api.Error{Operation: opCountryAvailabilityGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, nil, &api.Error{
			Operation:  opCountryAvailabilityGet,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	// Capture the read error on the success path (matching tracks.List /
	// listings / reviews): a partial body would otherwise reach
	// json.Unmarshal and surface as a "decode response" error that buries
	// the real (network) cause.
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, nil, &api.Error{
			Operation:  opCountryAvailabilityGet,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "read response: " + readErr.Error(),
			Cause:      readErr,
		}
	}
	var parsed TrackCountryAvailability
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, &api.Error{
			Operation:  opCountryAvailabilityGet,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return &parsed, raw, nil
}
