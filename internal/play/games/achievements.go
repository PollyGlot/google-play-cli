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

// AchievementConfigurationDetail is the editable draft / read-only published
// detail of an achievement. iconUrl and sortRank are output-only (writes are
// ignored by the API) but kept here so a verbatim view round-trips.
type AchievementConfigurationDetail struct {
	Kind        string                 `json:"kind,omitempty"`
	Name        *LocalizedStringBundle `json:"name,omitempty"`
	Description *LocalizedStringBundle `json:"description,omitempty"`
	// PointValue is a *int (not int) so an explicit zero round-trips: omitempty
	// drops a nil pointer (field genuinely unset) but emits a non-nil &0, which
	// a plain `int,omitempty` would silently drop: defeating the --point-value 0
	// write the gamescmd *Set flags exist to support.
	PointValue *int   `json:"pointValue,omitempty"`
	IconURL    string `json:"iconUrl,omitempty"`
	SortRank   int    `json:"sortRank,omitempty"`
}

// AchievementConfiguration is the API-shaped achievement resource. JSON tags
// mirror the API verbatim (ADR-0003). The draft is editable; published is
// read-only live state.
type AchievementConfiguration struct {
	Kind            string `json:"kind,omitempty"`
	ID              string `json:"id,omitempty"`
	AchievementType string `json:"achievementType,omitempty"`
	InitialState    string `json:"initialState,omitempty"`
	// StepsToUnlock is a *int for the same reason as PointValue: an explicit
	// --steps-to-unlock 0 must reach the wire, which a plain `int,omitempty`
	// would drop.
	StepsToUnlock *int                            `json:"stepsToUnlock,omitempty"`
	Draft         *AchievementConfigurationDetail `json:"draft,omitempty"`
	Published     *AchievementConfigurationDetail `json:"published,omitempty"`
	Token         string                          `json:"token,omitempty"`
}

// Detail returns the draft when present, else the published detail: the
// best-available source for the human views (a freshly created achievement
// has only a draft; a never-edited one read back may surface published).
func (a AchievementConfiguration) Detail() *AchievementConfigurationDetail {
	if a.Draft != nil {
		return a.Draft
	}
	return a.Published
}

// AchievementListResponse is the parsed AchievementConfigurationListResponse.
type AchievementListResponse struct {
	Kind          string                     `json:"kind,omitempty"`
	Items         []AchievementConfiguration `json:"items,omitempty"`
	NextPageToken string                     `json:"nextPageToken,omitempty"`
}

// ListAchievements returns the achievement configurations for an application.
// maxResults<=0 omits the paging cap; pageToken follows a previous page.
func ListAchievements(ctx context.Context, hc *http.Client, appID string, maxResults int, pageToken string) (AchievementListResponse, json.RawMessage, error) {
	req, err := newJSONReq(ctx, mAchList, opAchList, appID, map[string]string{"applicationId": appID}, listQuery(maxResults, pageToken), nil)
	if err != nil {
		return AchievementListResponse{}, nil, err
	}
	raw, err := do(hc, opAchList, appID, req)
	if err != nil {
		return AchievementListResponse{}, nil, err
	}
	var lr AchievementListResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return AchievementListResponse{}, nil, &api.Error{Operation: opAchList, Package: appID, Message: "decode response: " + err.Error(), Cause: err}
	}
	return lr, raw, nil
}

// GetAchievement reads a single achievement configuration by its ID.
func GetAchievement(ctx context.Context, hc *http.Client, achievementID string) (AchievementConfiguration, json.RawMessage, error) {
	req, err := newJSONReq(ctx, mAchGet, opAchGet, achievementID, map[string]string{"achievementId": achievementID}, "", nil)
	if err != nil {
		return AchievementConfiguration{}, nil, err
	}
	return doAchievement(hc, opAchGet, achievementID, req)
}

// CreateAchievement inserts a new achievement configuration in an application
// from the JSON body (an AchievementConfiguration), returning the created
// resource plus the verbatim response.
func CreateAchievement(ctx context.Context, hc *http.Client, appID string, body []byte) (AchievementConfiguration, json.RawMessage, error) {
	req, err := newJSONReq(ctx, mAchInsert, opAchInsert, appID, map[string]string{"applicationId": appID}, "", body)
	if err != nil {
		return AchievementConfiguration{}, nil, err
	}
	return doAchievement(hc, opAchInsert, appID, req)
}

// UpdateAchievement replaces the achievement configuration's metadata (PUT)
// from the JSON body, returning the updated resource plus the verbatim response.
func UpdateAchievement(ctx context.Context, hc *http.Client, achievementID string, body []byte) (AchievementConfiguration, json.RawMessage, error) {
	req, err := newJSONReq(ctx, mAchUpdate, opAchUpdate, achievementID, map[string]string{"achievementId": achievementID}, "", body)
	if err != nil {
		return AchievementConfiguration{}, nil, err
	}
	return doAchievement(hc, opAchUpdate, achievementID, req)
}

// DeleteAchievement deletes the achievement configuration with the given ID.
// The API returns no useful body, so only an error is reported.
func DeleteAchievement(ctx context.Context, hc *http.Client, achievementID string) error {
	req, err := newJSONReq(ctx, mAchDelete, opAchDelete, achievementID, map[string]string{"achievementId": achievementID}, "", nil)
	if err != nil {
		return err
	}
	_, err = do(hc, opAchDelete, achievementID, req)
	return err
}

// doAchievement executes req, parses the 2xx body as an AchievementConfiguration,
// and returns it with the verbatim raw body.
func doAchievement(hc *http.Client, op, ref string, req *http.Request) (AchievementConfiguration, json.RawMessage, error) {
	raw, err := do(hc, op, ref, req)
	if err != nil {
		return AchievementConfiguration{}, nil, err
	}
	var a AchievementConfiguration
	if err := json.Unmarshal(raw, &a); err != nil {
		return AchievementConfiguration{}, nil, &api.Error{Operation: op, Package: ref, Message: "decode response: " + err.Error(), Cause: err}
	}
	return a, raw, nil
}
