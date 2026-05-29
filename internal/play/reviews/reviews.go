// Package reviews reads the user reviews of a Google Play app via the
// reviews.list endpoint. Unlike tracks/releases it does NOT run inside an
// Edit — reviews are a direct read on the application. The API exposes only
// the last 7 days (docs/DESIGN.md §5); historical retrieval (GCS CSV) is in
// docs/BACKLOG.md. List owns auto-pagination; the client-side `--stars`
// filter and the 7-day stderr warning are command-layer concerns.
package reviews

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opReviewsList = "reviews.list"

// Timestamp mirrors the API's google.protobuf.Timestamp-shaped value:
// seconds is a decimal string, nanos an int. Modeled only where gplay reads
// it (a review's last-modified instant).
type Timestamp struct {
	Seconds string `json:"seconds"`
	Nanos   int    `json:"nanos"`
}

// UserComment is the API-shaped subset of a review's user comment gplay
// reads: the rating, the reviewer's locale, the body, and when it changed.
type UserComment struct {
	Text             string     `json:"text"`
	StarRating       int        `json:"starRating"`
	ReviewerLanguage string     `json:"reviewerLanguage"`
	LastModified     *Timestamp `json:"lastModified"`
}

// Comment is one entry in a review's comments array. The API interleaves
// user and developer comments; gplay reads the user comment.
type Comment struct {
	UserComment *UserComment `json:"userComment"`
}

// Review is the API-shaped Review resource, modeling only the fields gplay
// reads. Raw holds the verbatim JSON object so `--output json` can re-emit
// it untouched (ADR-0003), even after pagination merges several pages and
// the `--stars`/`--limit` filters narrow the set.
type Review struct {
	Raw      json.RawMessage `json:"-"`
	ReviewID string          `json:"reviewId"`
	Comments []Comment       `json:"comments"`
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
	if uc == nil || uc.LastModified == nil {
		return time.Time{}
	}
	secs, err := strconv.ParseInt(uc.LastModified.Seconds, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(secs, int64(uc.LastModified.Nanos)).UTC()
}

// List fetches every review of pkg from reviews.list, following
// tokenPagination.nextPageToken (carried back as the `token` query param)
// until the API stops returning one. It returns one Review per API entry,
// each retaining its verbatim JSON for the pass-through.
func List(ctx context.Context, hc *http.Client, pkg string) ([]Review, error) {
	var out []Review
	pageToken := ""
	// seen guards against a server that repeats or cycles a pagination
	// token: without this the loop would request the same page forever
	// (until context cancellation). A non-progressing token is treated as a
	// server fault rather than silently truncating the result.
	seen := map[string]struct{}{}
	for {
		reviews, next, err := listPage(ctx, hc, pkg, pageToken)
		if err != nil {
			return nil, err
		}
		out = append(out, reviews...)
		if next == "" {
			return out, nil
		}
		if _, dup := seen[next]; dup {
			return nil, &api.Error{
				Operation: opReviewsList,
				Package:   pkg,
				Message:   "pagination token loop detected in reviews.list (server repeated a nextPageToken)",
			}
		}
		seen[next] = struct{}{}
		pageToken = next
	}
}

// listPage fetches a single reviews.list page. pageToken is the prior page's
// nextPageToken ("" for the first call). It returns the page's reviews and
// the nextPageToken to continue with ("" when the page is the last).
func listPage(ctx context.Context, hc *http.Client, pkg, pageToken string) ([]Review, string, error) {
	u := api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) + "/reviews"
	if pageToken != "" {
		u += "?token=" + url.QueryEscape(pageToken)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", &api.Error{Operation: opReviewsList, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", &api.Error{Operation: opReviewsList, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, "", &api.Error{
			Operation:  opReviewsList,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, "", &api.Error{
			Operation:  opReviewsList,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "read response: " + readErr.Error(),
			Cause:      readErr,
		}
	}
	var page struct {
		Reviews         []json.RawMessage `json:"reviews"`
		TokenPagination struct {
			NextPageToken string `json:"nextPageToken"`
		} `json:"tokenPagination"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, "", &api.Error{
			Operation:  opReviewsList,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	reviews := make([]Review, 0, len(page.Reviews))
	for _, rm := range page.Reviews {
		var rv Review
		if err := json.Unmarshal(rm, &rv); err != nil {
			return nil, "", &api.Error{
				Operation:  opReviewsList,
				Package:    pkg,
				StatusCode: resp.StatusCode,
				Message:    "decode review: " + err.Error(),
				Cause:      err,
			}
		}
		rv.Raw = rm
		reviews = append(reviews, rv)
	}
	return reviews, page.TokenPagination.NextPageToken, nil
}
