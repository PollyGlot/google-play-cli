// Package gcs is gplay's minimal, resource-agnostic client for the Google
// Cloud Storage JSON API. It exists because the developer's monthly Play
// reports (reviews history, #94 / ADR-0037) live as plain GCS objects in the
// reporting bucket — a third Google service reached the same way as the second
// (raw HTTP, ADR-0007; a distinct read-only OAuth scope, ADR-0027). Only the
// two operations gplay needs are implemented: list the objects under a prefix
// and fetch one object's bytes. Both are reads — no mutating surface exists
// here (read-only by scope construction, token.StorageReadOnlyScope).
//
// The *http.Client is injected by the caller (kernel.AuthedClient), so tests
// mock the wire with a RoundTripper exactly as the androidpublisher calls do.
// Upstream failures surface as *api.Error so the shared Coder mapping applies
// (403 → exit 11, 404 → exit 30).
package gcs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	// StorageBase is the GCS JSON API base for bucket object operations:
	// {StorageBase}/{bucket}/o lists, {StorageBase}/{bucket}/o/{object} reads.
	StorageBase = "https://storage.googleapis.com/storage/v1/b"

	opObjectsList = "storage.objects.list"
	opObjectsGet  = "storage.objects.get"

	// maxListBodyRead / maxObjectRead bound the in-memory copy of a list page
	// and a fetched object. Monthly review CSVs are well under 64 MB even for
	// high-volume apps; the cap stops a hostile or misconfigured bucket from
	// exhausting RAM.
	maxListBodyRead = 4 * 1024 * 1024
	maxObjectRead   = 64 * 1024 * 1024
)

// Object is the sliver of a GCS object resource gplay reads — just its name
// (the full key under the bucket, e.g. "reviews/reviews_com.x_202606.csv").
type Object struct {
	Name string `json:"name"`
}

// ListObjects returns every object in bucket whose name starts with prefix,
// following the JSON API's pageToken pagination until exhausted. It is a read
// (storage.objects.list). A non-2xx becomes an *api.Error carrying the status
// (403 → exit 11, 404 → exit 30); the bucket stands in for the api.Error's
// Package field so the message names what was addressed.
func ListObjects(ctx context.Context, hc *http.Client, bucket, prefix string) ([]Object, error) {
	var out []Object
	pageToken := ""
	// seen guards against a server that repeats a pageToken forever.
	seen := map[string]struct{}{}
	for {
		items, next, err := listPage(ctx, hc, bucket, prefix, pageToken)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		if _, dup := seen[next]; dup {
			return nil, &api.Error{
				Operation: opObjectsList,
				Package:   bucket,
				Message:   "pagination token loop detected in storage.objects.list (server repeated a pageToken)",
			}
		}
		seen[next] = struct{}{}
		pageToken = next
	}
}

// listPage fetches one storage.objects.list page. pageToken is the prior
// page's nextPageToken ("" for the first call).
func listPage(ctx context.Context, hc *http.Client, bucket, prefix, pageToken string) ([]Object, string, error) {
	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	u := StorageBase + "/" + url.PathEscape(bucket) + "/o"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}

	raw, err := doGet(ctx, hc, opObjectsList, bucket, u, maxListBodyRead)
	if err != nil {
		return nil, "", err
	}
	var page struct {
		Items         []Object `json:"items"`
		NextPageToken string   `json:"nextPageToken"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, "", &api.Error{
			Operation: opObjectsList,
			Package:   bucket,
			Message:   "decode response: " + err.Error(),
			Cause:     err,
		}
	}
	return page.Items, page.NextPageToken, nil
}

// FetchObject returns the raw bytes of object in bucket via the JSON API's
// media download (?alt=media). It is a read (storage.objects.get). The object
// name is a single, un-split path segment — GCS keys legitimately contain
// slashes ("reviews/..."), so the whole name is percent-encoded into one path
// component rather than joined raw.
func FetchObject(ctx context.Context, hc *http.Client, bucket, object string) ([]byte, error) {
	u := StorageBase + "/" + url.PathEscape(bucket) + "/o/" + url.PathEscape(object) + "?alt=media"
	return doGet(ctx, hc, opObjectsGet, bucket, u, maxObjectRead)
}

// doGet performs a GET and returns the 2xx body, or an *api.Error carrying the
// status on a non-2xx (parsed through the shared Google error envelope) or a
// transport failure (status 0 → exit 50).
func doGet(ctx context.Context, hc *http.Client, op, bucket, u string, maxRead int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: bucket, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: bucket, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, &api.Error{
			Operation:  op,
			Package:    bucket,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxRead))
	if readErr != nil {
		return nil, &api.Error{
			Operation:  op,
			Package:    bucket,
			StatusCode: resp.StatusCode,
			Message:    "read response: " + readErr.Error(),
			Cause:      readErr,
		}
	}
	return raw, nil
}
