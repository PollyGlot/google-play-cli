package reviews

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opReviewsList  = "reviews.list"
	opReviewsReply = "reviews.reply"
	opReviewsGet   = "reviews.get"
)

const MaxReplyChars = 350

const listPageSize = "100"

var (
	mReviewsList  = apiregistry.MustResolve("androidpublisher.reviews.list")
	mReviewsReply = apiregistry.MustResolve("androidpublisher.reviews.reply")
	mReviewsGet   = apiregistry.MustResolve("androidpublisher.reviews.get")
)

type Timestamp struct {
	Seconds string `json:"seconds"`
	Nanos   int    `json:"nanos"`
}

func (ts *Timestamp) Time() time.Time {
	if ts == nil {
		return time.Time{}
	}
	secs, err := strconv.ParseInt(ts.Seconds, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(secs, int64(ts.Nanos)).UTC()
}

type UserComment struct {
	Text             string     `json:"text"`
	StarRating       int        `json:"starRating"`
	ReviewerLanguage string     `json:"reviewerLanguage"`
	LastModified     *Timestamp `json:"lastModified"`
	Device           string     `json:"device"`
	AndroidOSVersion int        `json:"androidOsVersion"`
	AppVersionName   string     `json:"appVersionName"`
	AppVersionCode   int        `json:"appVersionCode"`
}

type DeveloperComment struct {
	Text         string     `json:"text"`
	LastModified *Timestamp `json:"lastModified"`
}

type Comment struct {
	UserComment      *UserComment      `json:"userComment"`
	DeveloperComment *DeveloperComment `json:"developerComment"`
}

type Review struct {
	Raw        json.RawMessage `json:"-"`
	ReviewID   string          `json:"reviewId"`
	AuthorName string          `json:"authorName"`
	Comments   []Comment       `json:"comments"`
}

func (r Review) userComment() *UserComment {
	for _, c := range r.Comments {
		if c.UserComment != nil {
			return c.UserComment
		}
	}
	return nil
}

func (r Review) Stars() int {
	if uc := r.userComment(); uc != nil {
		return uc.StarRating
	}
	return 0
}

func (r Review) Locale() string {
	if uc := r.userComment(); uc != nil {
		return uc.ReviewerLanguage
	}
	return ""
}

func (r Review) Text() string {
	if uc := r.userComment(); uc != nil {
		return uc.Text
	}
	return ""
}

func (r Review) LastModified() time.Time {
	uc := r.userComment()
	if uc == nil {
		return time.Time{}
	}
	return uc.LastModified.Time()
}

func (r Review) Author() string { return r.AuthorName }

func (r Review) Device() string {
	if uc := r.userComment(); uc != nil {
		return uc.Device
	}
	return ""
}

func (r Review) AppVersion() string {
	uc := r.userComment()
	if uc == nil {
		return ""
	}
	switch {
	case uc.AppVersionName != "" && uc.AppVersionCode != 0:
		return uc.AppVersionName + " (" + strconv.Itoa(uc.AppVersionCode) + ")"
	case uc.AppVersionName != "":
		return uc.AppVersionName
	case uc.AppVersionCode != 0:
		return "(" + strconv.Itoa(uc.AppVersionCode) + ")"
	default:
		return ""
	}
}

func (r Review) DeveloperReplies() []DeveloperComment {
	var out []DeveloperComment
	for _, c := range r.Comments {
		if c.DeveloperComment != nil {
			out = append(out, *c.DeveloperComment)
		}
	}
	return out
}

func Reply(ctx context.Context, hc *http.Client, pkg, reviewID, text string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: mReviewsReply, Op: opReviewsReply, Target: pkg,
		Params: map[string]string{"packageName": pkg, "reviewId": reviewID},
		Body: struct {
			ReplyText string `json:"replyText"`
		}{ReplyText: text},
	})
}

func Get(ctx context.Context, hc *http.Client, pkg, reviewID string) (Review, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: mReviewsGet, Op: opReviewsGet, Target: pkg,
		Params: map[string]string{"packageName": pkg, "reviewId": reviewID},
	})
	if err != nil {
		return Review{}, err
	}
	var rv Review
	if err := json.Unmarshal(raw, &rv); err != nil {
		return Review{}, decodeError(opReviewsGet, pkg, "decode review: ", err)
	}
	rv.Raw = raw
	return rv, nil
}

func decodeError(op, pkg, what string, err error) error {
	return &api.Error{Operation: op, Package: pkg, StatusCode: http.StatusOK, Message: what + err.Error(), Cause: err}
}

func List(ctx context.Context, hc *http.Client, pkg string) ([]Review, error) {
	out, _, err := api.Paginate(api.Pager{Op: opReviewsList, Target: pkg, What: "reviews.list"},
		func(token string, _ int) ([]Review, string, error) { return listPage(ctx, hc, pkg, token) })
	return out, err
}

func listPage(ctx context.Context, hc *http.Client, pkg, pageToken string) ([]Review, string, error) {
	q := url.Values{"maxResults": {listPageSize}}
	if pageToken != "" {
		q.Set("token", pageToken)
	}
	raw, err := api.Do(ctx, hc, api.Call{
		Method: mReviewsList, Op: opReviewsList, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Query:  q,
	})
	if err != nil {
		return nil, "", err
	}
	var page struct {
		Reviews         []json.RawMessage `json:"reviews"`
		TokenPagination struct {
			NextPageToken string `json:"nextPageToken"`
		} `json:"tokenPagination"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, "", decodeError(opReviewsList, pkg, "decode response: ", err)
	}
	reviews := make([]Review, 0, len(page.Reviews))
	for _, rm := range page.Reviews {
		var rv Review
		if err := json.Unmarshal(rm, &rv); err != nil {
			return nil, "", decodeError(opReviewsList, pkg, "decode review: ", err)
		}
		rv.Raw = rm
		reviews = append(reviews, rv)
	}
	return reviews, page.TokenPagination.NextPageToken, nil
}
