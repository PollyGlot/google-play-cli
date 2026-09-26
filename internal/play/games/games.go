// Package games performs the hand-rolled HTTP calls for a game's Play Games
// Services configuration: the achievementConfigurations and
// leaderboardConfigurations resources of the gamesConfiguration service
// (ADR-0007 raw HTTP, ADR-0033). It is a DISTINCT Google service from the
// Android Publisher API: its own host (gamesconfiguration.googleapis.com,
// resolved from the registry) reached with the shared androidpublisher OAuth
// scope, addressed by the Play Games application ID (its own numeric ID space,
// not the Android package).
//
// Each resource exposes the same five methods (list/get/insert/update/delete).
// Writes affect the editable draft; the published detail is read-only and
// there is no publish method (publishing to players is Console-only). Each
// non-delete call returns a typed struct (for the table/markdown views) plus
// the verbatim response body (for the ADR-0003 --output json pass-through);
// every failure surfaces as *api.Error so the exit-code taxonomy maps
// transparently.
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

// Registry entries for the ten gamesConfiguration methods this package calls.
// Resolving at init turns an unregistered or vanished method into a CI panic
// rather than a runtime surprise; verb, host (gamesconfiguration.googleapis.com,
// not androidpublisher) and URL template then come from the Discovery snapshot
// instead of literals kept here (#513, batch 3).
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

// LocalizedString is one locale's value within a LocalizedStringBundle. JSON
// tags mirror the API verbatim for the ADR-0003 pass-through.
type LocalizedString struct {
	Kind   string `json:"kind,omitempty"`
	Locale string `json:"locale,omitempty"`
	Value  string `json:"value,omitempty"`
}

// LocalizedStringBundle is the set of per-locale strings the draft details
// carry for names/descriptions.
type LocalizedStringBundle struct {
	Kind         string            `json:"kind,omitempty"`
	Translations []LocalizedString `json:"translations,omitempty"`
}

// First returns the first translation's value (or "" when the bundle is nil or
// empty): the single string the table/markdown views show for a resource
// whose name is otherwise a per-locale bundle.
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

// listQuery encodes the shared paging parameters (maxResults/pageToken) the two
// list methods accept; maxResults<=0 omits the param (API default).
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

// call describes one request: m supplies verb and URL template, so a call site
// cannot pair one method's verb with another's path (#516). ref is the
// addressing context (application ID or resource ID) carried in api.Error for
// the human-readable message. body nil sends no body (GET/DELETE); anything
// else is a JSON write sent verbatim.
func call(m apiregistry.Method, op, ref string, params map[string]string, q url.Values, body []byte) api.Call {
	c := api.Call{Method: m, Op: op, Target: ref, Params: params, Query: q}
	if body != nil {
		c.Body = body
	}
	return c
}

// doJSON sends c and decodes the 2xx body into a T, returning it with the
// verbatim raw body.
func doJSON[T any](ctx context.Context, hc *http.Client, c api.Call) (T, json.RawMessage, error) {
	var out T
	raw, err := api.DoJSON(ctx, hc, c, &out)
	if err != nil {
		var zero T
		return zero, nil, err
	}
	return out, raw, nil
}
