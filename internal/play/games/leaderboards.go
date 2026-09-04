package games

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opLbList   = "leaderboardConfigurations.list"
	opLbGet    = "leaderboardConfigurations.get"
	opLbInsert = "leaderboardConfigurations.insert"
	opLbUpdate = "leaderboardConfigurations.update"
	opLbDelete = "leaderboardConfigurations.delete"
)

// LeaderboardConfigurationDetail is the editable draft / read-only published
// detail of a leaderboard. scoreFormat is kept as a raw message so the full
// number-format schema round-trips verbatim without being re-modelled here;
// iconUrl and sortRank are output-only (writes ignored).
type LeaderboardConfigurationDetail struct {
	Kind        string                 `json:"kind,omitempty"`
	Name        *LocalizedStringBundle `json:"name,omitempty"`
	ScoreFormat json.RawMessage        `json:"scoreFormat,omitempty"`
	IconURL     string                 `json:"iconUrl,omitempty"`
	SortRank    int                    `json:"sortRank,omitempty"`
}

// LeaderboardConfiguration is the API-shaped leaderboard resource. scoreMin and
// scoreMax are int64s the API encodes as JSON strings (format:int64), so they
// are typed as string here to round-trip verbatim (ADR-0003).
type LeaderboardConfiguration struct {
	Kind       string                          `json:"kind,omitempty"`
	ID         string                          `json:"id,omitempty"`
	ScoreOrder string                          `json:"scoreOrder,omitempty"`
	ScoreMin   string                          `json:"scoreMin,omitempty"`
	ScoreMax   string                          `json:"scoreMax,omitempty"`
	Draft      *LeaderboardConfigurationDetail `json:"draft,omitempty"`
	Published  *LeaderboardConfigurationDetail `json:"published,omitempty"`
	Token      string                          `json:"token,omitempty"`
}

// Detail returns the draft when present, else the published detail.
func (l LeaderboardConfiguration) Detail() *LeaderboardConfigurationDetail {
	if l.Draft != nil {
		return l.Draft
	}
	return l.Published
}

// LeaderboardListResponse is the parsed LeaderboardConfigurationListResponse.
type LeaderboardListResponse struct {
	Kind          string                     `json:"kind,omitempty"`
	Items         []LeaderboardConfiguration `json:"items,omitempty"`
	NextPageToken string                     `json:"nextPageToken,omitempty"`
}

// ListLeaderboards returns the leaderboard configurations for an application.
func ListLeaderboards(ctx context.Context, hc *http.Client, appID string, maxResults int, pageToken string) (LeaderboardListResponse, json.RawMessage, error) {
	req, err := newJSONReq(ctx, mLbList, opLbList, appID, map[string]string{"applicationId": appID}, listQuery(maxResults, pageToken), nil)
	if err != nil {
		return LeaderboardListResponse{}, nil, err
	}
	raw, err := do(hc, opLbList, appID, req)
	if err != nil {
		return LeaderboardListResponse{}, nil, err
	}
	var lr LeaderboardListResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return LeaderboardListResponse{}, nil, &api.Error{Operation: opLbList, Package: appID, Message: "decode response: " + err.Error(), Cause: err}
	}
	return lr, raw, nil
}

// GetLeaderboard reads a single leaderboard configuration by its ID.
func GetLeaderboard(ctx context.Context, hc *http.Client, leaderboardID string) (LeaderboardConfiguration, json.RawMessage, error) {
	req, err := newJSONReq(ctx, mLbGet, opLbGet, leaderboardID, map[string]string{"leaderboardId": leaderboardID}, "", nil)
	if err != nil {
		return LeaderboardConfiguration{}, nil, err
	}
	return doLeaderboard(hc, opLbGet, leaderboardID, req)
}

// CreateLeaderboard inserts a new leaderboard configuration in an application
// from the JSON body (a LeaderboardConfiguration).
func CreateLeaderboard(ctx context.Context, hc *http.Client, appID string, body []byte) (LeaderboardConfiguration, json.RawMessage, error) {
	req, err := newJSONReq(ctx, mLbInsert, opLbInsert, appID, map[string]string{"applicationId": appID}, "", body)
	if err != nil {
		return LeaderboardConfiguration{}, nil, err
	}
	return doLeaderboard(hc, opLbInsert, appID, req)
}

// UpdateLeaderboard replaces the leaderboard configuration's metadata (PUT)
// from the JSON body.
func UpdateLeaderboard(ctx context.Context, hc *http.Client, leaderboardID string, body []byte) (LeaderboardConfiguration, json.RawMessage, error) {
	req, err := newJSONReq(ctx, mLbUpdate, opLbUpdate, leaderboardID, map[string]string{"leaderboardId": leaderboardID}, "", body)
	if err != nil {
		return LeaderboardConfiguration{}, nil, err
	}
	return doLeaderboard(hc, opLbUpdate, leaderboardID, req)
}

// DeleteLeaderboard deletes the leaderboard configuration with the given ID.
func DeleteLeaderboard(ctx context.Context, hc *http.Client, leaderboardID string) error {
	req, err := newJSONReq(ctx, mLbDelete, opLbDelete, leaderboardID, map[string]string{"leaderboardId": leaderboardID}, "", nil)
	if err != nil {
		return err
	}
	_, err = do(hc, opLbDelete, leaderboardID, req)
	return err
}

// doLeaderboard executes req, parses the 2xx body as a LeaderboardConfiguration,
// and returns it with the verbatim raw body.
func doLeaderboard(hc *http.Client, op, ref string, req *http.Request) (LeaderboardConfiguration, json.RawMessage, error) {
	raw, err := do(hc, op, ref, req)
	if err != nil {
		return LeaderboardConfiguration{}, nil, err
	}
	var l LeaderboardConfiguration
	if err := json.Unmarshal(raw, &l); err != nil {
		return LeaderboardConfiguration{}, nil, &api.Error{Operation: op, Package: ref, Message: "decode response: " + err.Error(), Cause: err}
	}
	return l, raw, nil
}
