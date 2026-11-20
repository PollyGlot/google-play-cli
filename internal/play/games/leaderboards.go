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

type LeaderboardConfigurationDetail struct {
	Kind        string                 `json:"kind,omitempty"`
	Name        *LocalizedStringBundle `json:"name,omitempty"`
	ScoreFormat json.RawMessage        `json:"scoreFormat,omitempty"`
	IconURL     string                 `json:"iconUrl,omitempty"`
	SortRank    int                    `json:"sortRank,omitempty"`
}

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

func (l LeaderboardConfiguration) Detail() *LeaderboardConfigurationDetail {
	if l.Draft != nil {
		return l.Draft
	}
	return l.Published
}

type LeaderboardListResponse struct {
	Kind          string                     `json:"kind,omitempty"`
	Items         []LeaderboardConfiguration `json:"items,omitempty"`
	NextPageToken string                     `json:"nextPageToken,omitempty"`
}

func ListLeaderboards(ctx context.Context, hc *http.Client, appID string, maxResults int, pageToken string) (LeaderboardListResponse, json.RawMessage, error) {
	return doJSON[LeaderboardListResponse](ctx, hc, call(mLbList, opLbList, gamesApp(appID), map[string]string{"applicationId": appID}, listQuery(maxResults, pageToken), nil))
}

func GetLeaderboard(ctx context.Context, hc *http.Client, leaderboardID string) (LeaderboardConfiguration, json.RawMessage, error) {
	return doJSON[LeaderboardConfiguration](ctx, hc, call(mLbGet, opLbGet, leaderboard(leaderboardID), map[string]string{"leaderboardId": leaderboardID}, nil, nil))
}

func CreateLeaderboard(ctx context.Context, hc *http.Client, appID string, body []byte) (LeaderboardConfiguration, json.RawMessage, error) {
	return doJSON[LeaderboardConfiguration](ctx, hc, call(mLbInsert, opLbInsert, gamesApp(appID), map[string]string{"applicationId": appID}, nil, body))
}

func UpdateLeaderboard(ctx context.Context, hc *http.Client, leaderboardID string, body []byte) (LeaderboardConfiguration, json.RawMessage, error) {
	return doJSON[LeaderboardConfiguration](ctx, hc, call(mLbUpdate, opLbUpdate, leaderboard(leaderboardID), map[string]string{"leaderboardId": leaderboardID}, nil, body))
}

func DeleteLeaderboard(ctx context.Context, hc *http.Client, leaderboardID string) error {
	_, err := api.Do(ctx, hc, call(mLbDelete, opLbDelete, leaderboard(leaderboardID), map[string]string{"leaderboardId": leaderboardID}, nil, nil))
	return err
}
