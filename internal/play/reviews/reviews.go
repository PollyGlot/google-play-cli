// Package reviews reads the user reviews of a Google Play app via the
// reviews.list endpoint. Unlike tracks/releases it does NOT run inside an
// Edit: reviews are a direct read on the application. The API exposes only
// the last 7 days (docs/DESIGN.md §5); historical retrieval (GCS CSV) lives
// in `reviews history` (ADR-0037). List owns auto-pagination; the `--stars`
// filter and the 7-day stderr warning are command-layer concerns.
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

// Registry entries for the three methods this package calls. Resolving at init
// turns an unregistered or vanished method into a CI panic rather than a
// runtime surprise; verb and URL template then come from the Discovery
// snapshot instead of literals kept here (#513, batch 3).
var (
	mReviewsList  = apiregistry.MustResolve("androidpublisher.reviews.list")
	mReviewsReply = apiregistry.MustResolve("androidpublisher.reviews.reply")
	mReviewsGet   = apiregistry.MustResolve("androidpublisher.reviews.get")
)

// Timestamp mirrors the API's google.protobuf.Timestamp-shaped value:
// seconds is a decimal string, nanos an int. Modeled only where gplay reads
// it (a review's last-modified instant).
type Timestamp struct {
	Seconds string `json:"seconds"`
	Nanos   int    `json:"nanos"`
}

// Time returns the instant in UTC, or the zero Time when the Timestamp is
// absent (nil receiver) or its seconds field is unparseable.
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

// UserComment is the API-shaped subset of a review's user comment gplay
// reads: the rating, the reviewer's locale, the body, when it changed, and:
// for the deep single-review view: the device and app-version context.
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

// DeveloperComment is the API-shaped developer reply in a review's
// conversation thread: the reply body and when it last changed.
type DeveloperComment struct {
	Text         string     `json:"text"`
	LastModified *Timestamp `json:"lastModified"`
}

// Comment is one entry in a review's comments array. The API interleaves
// user and developer comments; gplay reads both: the user comment carries
// the rating/locale/text, the developer comment(s) the reply thread.
type Comment struct {
	UserComment      *UserComment      `json:"userComment"`
	DeveloperComment *DeveloperComment `json:"developerComment"`
}

// Review is the API-shaped Review resource, modeling only the fields gplay
// reads. Raw holds the verbatim JSON object so `--output json` can re-emit
// it untouched (ADR-0003), even after pagination merges several pages and
// the `--stars`/`--limit` filters narrow the set.
type Review struct {
	Raw        json.RawMessage `json:"-"`
	ReviewID   string          `json:"reviewId"`
	AuthorName string          `json:"authorName"`
	Comments   []Comment       `json:"comments"`
}

// userComment returns the review's first user comment (the one carrying the
// rating, locale, and text), or nil when the review has none.
func (r Review) userComment() *UserComment {
	for _, c := range r.Comments {
		if c.UserComment != nil {
			return c.UserComment
		}
	}
	return nil
}

// Stars is the review's rating (1..5), or 0 when there is no user comment.
func (r Review) Stars() int {
	if uc := r.userComment(); uc != nil {
		return uc.StarRating
	}
	return 0
}

// Locale is the reviewer's language tag (e.g. "en", "fr-FR"), or "".
func (r Review) Locale() string {
	if uc := r.userComment(); uc != nil {
		return uc.ReviewerLanguage
	}
	return ""
}

// Text is the body of the user comment, or "".
func (r Review) Text() string {
	if uc := r.userComment(); uc != nil {
		return uc.Text
	}
	return ""
}

// LastModified is the UTC instant the user comment last changed, or the zero
// Time when it is absent or unparseable.
func (r Review) LastModified() time.Time {
	uc := r.userComment()
	if uc == nil {
		return time.Time{}
	}
	return uc.LastModified.Time()
}

// Author is the display name of the reviewer, or "" when absent.
func (r Review) Author() string { return r.AuthorName }

// Device is the reviewer's device codename (e.g. "flame"), or "".
func (r Review) Device() string {
	if uc := r.userComment(); uc != nil {
		return uc.Device
	}
	return ""
}

// AppVersion is the app version the review was written against, rendered as
// "name (code)" when both are present, "name" or "(code)" when only one is,
// and "" when neither is. The version code alone is still useful, so it is
// surfaced even without a name.
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

// DeveloperReplies returns the developer comments in conversation order: the
// reply thread beneath the user's review. Empty when the developer has not
// responded.
func (r Review) DeveloperReplies() []DeveloperComment {
	var out []DeveloperComment
	for _, c := range r.Comments {
		if c.DeveloperComment != nil {
			out = append(out, *c.DeveloperComment)
		}
	}
	return out
}

// Reply posts a developer response to reviewID via reviews.reply. Like
// List it is a direct call on the application: reviews are NOT mutated
// inside an Edit. The request body is the API's {"replyText": ...} shape;
// the verbatim 2xx body is returned for the --output json pass-through
// (ADR-0003). A non-2xx becomes an *api.Error carrying the status, so the
// shared classifier maps 403 → exit 11 and 404 → exit 30.
func Reply(ctx context.Context, hc *http.Client, pkg, reviewID, text string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: mReviewsReply, Op: opReviewsReply, Target: pkg,
		Params: map[string]string{"packageName": pkg, "reviewId": reviewID},
		Body: struct {
			ReplyText string `json:"replyText"`
		}{ReplyText: text},
	})
}

// Get fetches a single review by reviewID via reviews.get. Like List/Reply it
// is a direct read on the application: reviews are NOT read inside an Edit.
// The returned Review keeps its verbatim JSON in Raw for the `--output json`
// pass-through (ADR-0003). A non-2xx becomes an *api.Error carrying the
// status, so the shared classifier maps 403 → exit 11 and 404 → exit 30, and
// a 404 here means an unknown OR expired reviewId (a valid review that has
// fallen out of the API's 7-day window).
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

// decodeError tags a 2xx body that does not decode. It keeps the status tag
// (exit 30) this module always gave it, unlike api.DoJSON's status-less one.
func decodeError(op, pkg, what string, err error) error {
	return &api.Error{Operation: op, Package: pkg, StatusCode: http.StatusOK, Message: what + err.Error(), Cause: err}
}

// List fetches every review of pkg from reviews.list, following
// tokenPagination.nextPageToken (carried back as the `token` query param)
// until the API stops returning one. It returns one Review per API entry,
// each retaining its verbatim JSON for the pass-through. A server that repeats
// or cycles a token fails the walk rather than silently truncating it.
func List(ctx context.Context, hc *http.Client, pkg string) ([]Review, error) {
	out, _, err := api.Paginate(api.Pager{Op: opReviewsList, Target: pkg, What: "reviews.list"},
		func(token string, _ int) ([]Review, string, error) { return listPage(ctx, hc, pkg, token) })
	return out, err
}

// listPage fetches a single reviews.list page. pageToken is the prior page's
// nextPageToken ("" for the first call). It returns the page's reviews and
// the nextPageToken to continue with ("" when the page is the last).
func listPage(ctx context.Context, hc *http.Client, pkg, pageToken string) ([]Review, string, error) {
	var q url.Values
	if pageToken != "" {
		q = url.Values{"token": {pageToken}}
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
