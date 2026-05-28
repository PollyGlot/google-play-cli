// Package tracks reads and updates the release configuration of Google
// Play tracks within an open Edit. The operations exposed — Get (one
// track), List (every track), and Update — are shared by the upload,
// promote, rollout, halt, resume, complete, and tracks-list commands.
package tracks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opTracksGet    = "tracks.get"
	opTracksList   = "tracks.list"
	opTracksUpdate = "tracks.update"
)

// LocalizedText mirrors the API's `LocalizedText` shape: one per locale
// in a release's releaseNotes payload.
type LocalizedText struct {
	Language string `json:"language"`
	Text     string `json:"text"`
}

// Release is the API-shaped subset of fields we set on a release. The
// json tags mirror the Google Play Developer API verbatim per
// ADR-0003 (JSON output is API pass-through).
type Release struct {
	Name         string          `json:"name,omitempty"`
	Status       string          `json:"status,omitempty"`
	UserFraction float64         `json:"userFraction,omitempty"`
	VersionCodes []string        `json:"versionCodes,omitempty"`
	ReleaseNotes []LocalizedText `json:"releaseNotes,omitempty"`
}

// Track is the API-shaped Track resource. Only the fields gplay reads
// or writes are modeled here.
type Track struct {
	Track    string    `json:"track"`
	Releases []Release `json:"releases"`
}

// Get fetches the current Track resource at edits.tracks.get. The
// returned Track carries every release coexisting on the track (e.g.
// inProgress + halted), which is what the promote / rollout / halt /
// resume verbs consume. The raw JSON body is returned alongside for
// --output json pass-through (ADR-0003) and for diagnostics.
func Get(ctx context.Context, hc *http.Client, pkg, editID, track string) (*Track, json.RawMessage, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/tracks/" + url.PathEscape(track)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, &api.Error{Operation: opTracksGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &api.Error{Operation: opTracksGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, nil, &api.Error{
			Operation:  opTracksGet,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	var parsed Track
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, &api.Error{
			Operation:  opTracksGet,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return &parsed, raw, nil
}

// List fetches every track configured on the app at edits.tracks.list:
// the standard tracks the SA can see plus any custom closed tracks. It
// returns the parsed Track slice and the raw {"tracks":[...]} body for
// the --output json pass-through (ADR-0003). Cross-track presentation
// (injecting absent standard tracks, deriving the standard/custom kind,
// summarizing the top release) is a command-layer concern; List returns
// exactly what the API returns. Like Get, it runs inside an Edit the
// caller has already opened.
func List(ctx context.Context, hc *http.Client, pkg, editID string) ([]Track, json.RawMessage, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/tracks"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, &api.Error{Operation: opTracksList, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &api.Error{Operation: opTracksList, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, nil, &api.Error{
			Operation:  opTracksList,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, nil, &api.Error{
			Operation:  opTracksList,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "read response: " + readErr.Error(),
			Cause:      readErr,
		}
	}
	var parsed struct {
		Tracks []Track `json:"tracks"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, &api.Error{
			Operation:  opTracksList,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return parsed.Tracks, raw, nil
}

// Update PUTs the track resource at edits.tracks.update carrying a single
// release. Returns the parsed Track and the raw JSON body for --output json
// pass-through (ADR-0003). Used by upload / promote, which build a fresh
// release for the (possibly empty) destination track.
func Update(ctx context.Context, hc *http.Client, pkg, editID, track string, release Release) (*Track, json.RawMessage, error) {
	payload, err := json.Marshal(Track{Track: track, Releases: []Release{release}})
	if err != nil {
		return nil, nil, &api.Error{Operation: opTracksUpdate, Package: pkg, Message: "marshal payload: " + err.Error(), Cause: err}
	}
	return UpdateRaw(ctx, hc, pkg, editID, track, payload)
}

// UpdateRaw PUTs a caller-built track body to edits.tracks.update verbatim.
// tracks.update REPLACES the track's whole releases array, so a caller that
// mutates one release on a track holding several (the rollout state machine)
// must send every release — and must preserve fields gplay does not model
// (e.g. countryTargeting). Building the body from the raw tracks.get JSON
// and patching only the target release is the lossless way to do that;
// round-tripping through the typed Release struct would drop the rest.
func UpdateRaw(ctx context.Context, hc *http.Client, pkg, editID, track string, body []byte) (*Track, json.RawMessage, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/tracks/" + url.PathEscape(track)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return nil, nil, &api.Error{Operation: opTracksUpdate, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &api.Error{Operation: opTracksUpdate, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, nil, &api.Error{
			Operation:  opTracksUpdate,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	// The success body is the ADR-0003 JSON pass-through; cap it at the
	// generous MaxAPISuccessBodyRead so apps with many locales × long
	// release notes don't get a silently truncated tracks.update response.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	var parsed Track
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, &api.Error{
			Operation:  opTracksUpdate,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return &parsed, raw, nil
}
