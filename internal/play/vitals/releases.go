package vitals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opReleaseFilterOptions = "playdeveloperreporting.apps.fetchReleaseFilterOptions"

// mReleaseFilterOptions is resolved at init like mListAnomalies: an
// unregistered or vanished method is a CI panic, not a runtime surprise.
var mReleaseFilterOptions = apiregistry.MustResolve(opReleaseFilterOptions)

// FetchReleaseFilterOptions issues apps.fetchReleaseFilterOptions (GET) for pkg
// and returns the body verbatim (ADR-0003): the tracks, their serving releases
// and the version codes those releases carry, i.e. the versionCode values a
// vitals filter can usefully name (#348). Read-only, reporting scope.
func FetchReleaseFilterOptions(ctx context.Context, hc *http.Client, pkg string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{Method: mReleaseFilterOptions, Op: opReleaseFilterOptions, Target: pkg, Params: map[string]string{"appsId": pkg}})
}

// Release is one row of the flattened release filter options: a serving
// release of a track and its version codes. A track serving no release yields
// one row with an empty Release, so the table still shows the track exists.
type Release struct {
	Track        string
	TrackType    string
	Release      string
	VersionCodes []string
}

// ParseReleases projects a fetchReleaseFilterOptions body into Release rows,
// in API order (tracks, then their serving releases). It feeds the table and
// markdown renderers and the --version-code completion; JSON stays verbatim.
func ParseReleases(body []byte) ([]Release, error) {
	var resp struct {
		Tracks []struct {
			DisplayName     string `json:"displayName"`
			Type            string `json:"type"`
			ServingReleases []struct {
				DisplayName  string   `json:"displayName"`
				VersionCodes []string `json:"versionCodes"`
			} `json:"servingReleases"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode release filter options: %w", err)
	}
	var out []Release
	for _, tr := range resp.Tracks {
		if len(tr.ServingReleases) == 0 {
			out = append(out, Release{Track: tr.DisplayName, TrackType: tr.Type})
			continue
		}
		for _, r := range tr.ServingReleases {
			out = append(out, Release{Track: tr.DisplayName, TrackType: tr.Type, Release: r.DisplayName, VersionCodes: r.VersionCodes})
		}
	}
	return out, nil
}
