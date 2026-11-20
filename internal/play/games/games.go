package games

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

var (
	mAchList   = apiregistry.MustResolve("gamesConfiguration.achievementConfigurations.list")
	mAchGet    = apiregistry.MustResolve("gamesConfiguration.achievementConfigurations.get")
	mAchInsert = apiregistry.MustResolve("gamesConfiguration.achievementConfigurations.insert")
	mAchUpdate = apiregistry.MustResolve("gamesConfiguration.achievementConfigurations.update")
	mAchDelete = apiregistry.MustResolve("gamesConfiguration.achievementConfigurations.delete")

	mLbList   = apiregistry.MustResolve("gamesConfiguration.leaderboardConfigurations.list")
	mLbGet    = apiregistry.MustResolve("gamesConfiguration.leaderboardConfigurations.get")
	mLbInsert = apiregistry.MustResolve("gamesConfiguration.leaderboardConfigurations.insert")
	mLbUpdate = apiregistry.MustResolve("gamesConfiguration.leaderboardConfigurations.update")
	mLbDelete = apiregistry.MustResolve("gamesConfiguration.leaderboardConfigurations.delete")
)

type LocalizedString struct {
	Kind   string `json:"kind,omitempty"`
	Locale string `json:"locale,omitempty"`
	Value  string `json:"value,omitempty"`
}

type LocalizedStringBundle struct {
	Kind         string            `json:"kind,omitempty"`
	Translations []LocalizedString `json:"translations,omitempty"`
}

func (b *LocalizedStringBundle) First() string {
	if b == nil {
		return ""
	}
	for _, t := range b.Translations {
		if t.Value != "" {
			return t.Value
		}
	}
	return ""
}

func listQuery(maxResults int, pageToken string) url.Values {
	q := url.Values{}
	if maxResults > 0 {
		q.Set("maxResults", strconv.Itoa(maxResults))
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	return q
}

func call(m apiregistry.Method, op string, ref api.Resource, params map[string]string, q url.Values, body []byte) api.Call {
	c := api.Call{Method: m, Op: op, Resource: ref, Params: params, Query: q}
	if body != nil {
		c.Body = body
	}
	return c
}

func doJSON[T any](ctx context.Context, hc *http.Client, c api.Call) (T, json.RawMessage, error) {
	var out T
	raw, err := api.DoJSON(ctx, hc, c, &out)
	if err != nil {
		var zero T
		return zero, nil, err
	}
	return out, raw, nil
}

func gamesApp(appID string) api.Resource {
	return api.Resource{Kind: api.KindGamesApplication, ID: appID}
}

func achievement(id string) api.Resource {
	return api.Resource{Kind: api.KindAchievement, ID: id}
}

func leaderboard(id string) api.Resource {
	return api.Resource{Kind: api.KindLeaderboard, ID: id}
}
