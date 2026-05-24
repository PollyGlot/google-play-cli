// Package tracks reads and updates the release configuration of a
// Google Play track within an open Edit. The two operations exposed —
// Get and Update — are shared by the upload, promote, rollout, halt,
// resume, complete, and tracks-list commands.
package tracks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
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

// Update PUTs the track resource at edits.tracks.update with the
// provided release. Returns the parsed Track and the raw JSON body for
// --output json pass-through (ADR-0003).
func Update(ctx context.Context, hc *http.Client, pkg, editID, track string, release Release) (*Track, json.RawMessage, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/tracks/" + url.PathEscape(track)

	payload, err := json.Marshal(Track{Track: track, Releases: []Release{release}})
	if err != nil {
		return nil, nil, fmt.Errorf("tracks.update: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("tracks.update: HTTP %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIBodyRead))
	var parsed Track
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, fmt.Errorf("tracks.update: decode response: %w", err)
	}
	return &parsed, raw, nil
}
