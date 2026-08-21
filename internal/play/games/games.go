// Package games performs the hand-rolled HTTP calls for a game's Play Games
// Services configuration: the achievementConfigurations and
// leaderboardConfigurations resources of the gamesConfiguration service
// (ADR-0007 raw HTTP, ADR-0033). It is a DISTINCT Google service from the
// Android Publisher API: its own host (api.GamesConfigBase) reached with the
// shared androidpublisher OAuth scope, addressed by the Play Games application
// ID (its own numeric ID space, not the Android package).
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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
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

// achievementsAppBase / achievementBase build the two URL shapes the
// achievementConfigurations methods use: list/insert hang off the application
// ID, get/update/delete off the achievement ID.
func achievementsAppBase(appID string) string {
	return api.GamesConfigBase + "/applications/" + url.PathEscape(appID) + "/achievements"
}

func achievementBase(achievementID string) string {
	return api.GamesConfigBase + "/achievements/" + url.PathEscape(achievementID)
}

// leaderboardsAppBase / leaderboardBase mirror the achievement helpers for the
// leaderboardConfigurations resource.
func leaderboardsAppBase(appID string) string {
	return api.GamesConfigBase + "/applications/" + url.PathEscape(appID) + "/leaderboards"
}

func leaderboardBase(leaderboardID string) string {
	return api.GamesConfigBase + "/leaderboards/" + url.PathEscape(leaderboardID)
}

// listQuery appends the shared paging parameters (maxResults/pageToken) the two
// list methods accept; maxResults<=0 omits the param (API default).
func listQuery(base string, maxResults int, pageToken string) string {
	q := url.Values{}
	if maxResults > 0 {
		q.Set("maxResults", strconv.Itoa(maxResults))
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	if enc := q.Encode(); enc != "" {
		return base + "?" + enc
	}
	return base
}

// newJSONReq builds a request with a JSON body (POST/PUT writes) or no body
// (GET/DELETE), wrapping a construction failure as *api.Error.
func newJSONReq(ctx context.Context, op, ref, method, u string, body []byte) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: ref, Message: err.Error(), Cause: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do runs req and maps the response to (raw body, *api.Error). ref is the
// addressing context (application ID or resource ID) carried in api.Error for
// the human-readable message.
func do(hc *http.Client, op, ref string, req *http.Request) (json.RawMessage, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: ref, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, &api.Error{Operation: op, Package: ref, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: op, Package: ref, StatusCode: resp.StatusCode, Message: "read response body: " + readErr.Error(), Cause: readErr}
	}
	return json.RawMessage(raw), nil
}
