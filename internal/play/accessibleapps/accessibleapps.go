package accessibleapps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opSearch = "playdeveloperreporting.apps.search"

var mSearch = apiregistry.MustResolve("playdeveloperreporting.apps.search")

type App struct {
	Name        string `json:"name,omitempty"`
	PackageName string `json:"packageName,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type SearchResponse struct {
	Apps          []App  `json:"apps,omitempty"`
	NextPageToken string `json:"nextPageToken,omitempty"`
}

func Search(ctx context.Context, hc *http.Client, pageSize int, pageToken string) (SearchResponse, json.RawMessage, error) {
	q := url.Values{}
	if pageSize > 0 {
		q.Set("pageSize", strconv.Itoa(pageSize))
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	var sr SearchResponse
	raw, err := api.DoJSON(ctx, hc, api.Call{Method: mSearch, Op: opSearch, Query: q}, &sr)
	if err != nil {
		return SearchResponse{}, nil, err
	}
	return sr, raw, nil
}
