package gcs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	StorageBase = "https://storage.googleapis.com/storage/v1/b"

	opObjectsList = "storage.objects.list"
	opObjectsGet  = "storage.objects.get"

	maxListBodyRead = 4 * 1024 * 1024
	MaxObjectRead   = 64 * 1024 * 1024
)

var ErrTooLarge = errors.New("object over the read cap")

type Object struct {
	Name    string `json:"name"`
	Size    string `json:"size,omitempty"`
	Updated string `json:"updated,omitempty"`
}

func ListObjects(ctx context.Context, hc *http.Client, bucket, prefix string) ([]Object, error) {
	out, _, err := api.Paginate(api.Pager{Op: opObjectsList, Resource: bucketResource(bucket), What: "storage.objects.list"},
		func(token string, _ int) ([]Object, string, error) { return listPage(ctx, hc, bucket, prefix, token) })
	return out, err
}

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
			Resource:  bucketResource(bucket),
			Message:   "decode response: " + err.Error(),
			Cause:     err,
		}
	}
	return page.Items, page.NextPageToken, nil
}

func FetchObject(ctx context.Context, hc *http.Client, bucket, object string) ([]byte, error) {
	return FetchObjectLimit(ctx, hc, bucket, object, MaxObjectRead)
}

func FetchObjectLimit(ctx context.Context, hc *http.Client, bucket, object string, maxBytes int64) ([]byte, error) {
	u := StorageBase + "/" + url.PathEscape(bucket) + "/o/" + url.PathEscape(object) + "?alt=media"
	return doGet(ctx, hc, opObjectsGet, bucket, u, maxBytes)
}

func doGet(ctx context.Context, hc *http.Client, op, bucket, u string, maxRead int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, &api.Error{Operation: op, Resource: bucketResource(bucket), Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Resource: bucketResource(bucket), Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, &api.Error{
			Operation:  op,
			Resource:   bucketResource(bucket),
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxRead+1))
	if readErr != nil {
		return nil, &api.Error{
			Operation: op,
			Resource:  bucketResource(bucket),
			Message:   "read response: " + readErr.Error(),
			Cause:     readErr,
		}
	}
	if int64(len(raw)) > maxRead {
		return nil, &api.Error{
			Operation:  op,
			Resource:   bucketResource(bucket),
			StatusCode: resp.StatusCode,
			Message:    "response exceeds size limit",
			Cause:      ErrTooLarge,
		}
	}
	return raw, nil
}

func bucketResource(bucket string) api.Resource {
	return api.Resource{Kind: api.KindBucket, ID: bucket}
}
