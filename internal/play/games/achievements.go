package games

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opAchList   = "achievementConfigurations.list"
	opAchGet    = "achievementConfigurations.get"
	opAchInsert = "achievementConfigurations.insert"
	opAchUpdate = "achievementConfigurations.update"
	opAchDelete = "achievementConfigurations.delete"
)

type AchievementConfigurationDetail struct {
	Kind        string                 `json:"kind,omitempty"`
	Name        *LocalizedStringBundle `json:"name,omitempty"`
	Description *LocalizedStringBundle `json:"description,omitempty"`
	PointValue  *int                   `json:"pointValue,omitempty"`
	IconURL     string                 `json:"iconUrl,omitempty"`
	SortRank    int                    `json:"sortRank,omitempty"`
}

type AchievementConfiguration struct {
	Kind            string                          `json:"kind,omitempty"`
	ID              string                          `json:"id,omitempty"`
	AchievementType string                          `json:"achievementType,omitempty"`
	InitialState    string                          `json:"initialState,omitempty"`
	StepsToUnlock   *int                            `json:"stepsToUnlock,omitempty"`
	Draft           *AchievementConfigurationDetail `json:"draft,omitempty"`
	Published       *AchievementConfigurationDetail `json:"published,omitempty"`
	Token           string                          `json:"token,omitempty"`
}

func (a AchievementConfiguration) Detail() *AchievementConfigurationDetail {
	if a.Draft != nil {
		return a.Draft
	}
	return a.Published
}

type AchievementListResponse struct {
	Kind          string                     `json:"kind,omitempty"`
	Items         []AchievementConfiguration `json:"items,omitempty"`
	NextPageToken string                     `json:"nextPageToken,omitempty"`
}

func ListAchievements(ctx context.Context, hc *http.Client, appID string, maxResults int, pageToken string) (AchievementListResponse, json.RawMessage, error) {
	return doJSON[AchievementListResponse](ctx, hc, call(mAchList, opAchList, gamesApp(appID), map[string]string{"applicationId": appID}, listQuery(maxResults, pageToken), nil))
}

func GetAchievement(ctx context.Context, hc *http.Client, achievementID string) (AchievementConfiguration, json.RawMessage, error) {
	return doJSON[AchievementConfiguration](ctx, hc, call(mAchGet, opAchGet, achievement(achievementID), map[string]string{"achievementId": achievementID}, nil, nil))
}

func CreateAchievement(ctx context.Context, hc *http.Client, appID string, body []byte) (AchievementConfiguration, json.RawMessage, error) {
	return doJSON[AchievementConfiguration](ctx, hc, call(mAchInsert, opAchInsert, gamesApp(appID), map[string]string{"applicationId": appID}, nil, body))
}

func UpdateAchievement(ctx context.Context, hc *http.Client, achievementID string, body []byte) (AchievementConfiguration, json.RawMessage, error) {
	return doJSON[AchievementConfiguration](ctx, hc, call(mAchUpdate, opAchUpdate, achievement(achievementID), map[string]string{"achievementId": achievementID}, nil, body))
}

func DeleteAchievement(ctx context.Context, hc *http.Client, achievementID string) error {
	_, err := api.Do(ctx, hc, call(mAchDelete, opAchDelete, achievement(achievementID), map[string]string{"achievementId": achievementID}, nil, nil))
	return err
}
