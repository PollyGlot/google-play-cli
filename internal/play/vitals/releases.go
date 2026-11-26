package vitals

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opReleaseFilterOptions = "playdeveloperreporting.apps.fetchReleaseFilterOptions"

var mReleaseFilterOptions = apiregistry.MustResolve(opReleaseFilterOptions)

func FetchReleaseFilterOptions(ctx context.Context, hc *http.Client, pkg string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{Method: mReleaseFilterOptions, Op: opReleaseFilterOptions, Target: pkg, Params: map[string]string{"appsId": pkg}})
}

type Release struct {
	Track        string
	TrackType    string
	Release      string
	VersionCodes []string
}

func ParseReleases(pkg string, body []byte) ([]Release, error) {
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
		return nil, api.DecodeFailure(api.Error{Operation: opReleaseFilterOptions, Package: pkg, Message: "decode release filter options: " + err.Error(), Cause: err})
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
