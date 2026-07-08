package gcs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// rt is a RoundTripper that records the requests it sees and serves canned
// responses keyed by whether the path is a list (…/o) or a media fetch (…/o/…).
type rt struct {
	calls    []string
	listBody string
	getBody  string
	code     int // 0 -> 200
	errBody  string
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls = append(r.calls, req.Method+" "+req.URL.Host+req.URL.EscapedPath()+"?"+req.URL.RawQuery)
	code := r.code
	if code == 0 {
		code = 200
	}
	body := ""
	switch {
	case r.code != 0:
		body = r.errBody
	case strings.Contains(req.URL.Path, "/o/"): // media fetch
		body = r.getBody
	default:
		body = r.listBody
	}
	h := make(http.Header)
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: h}, nil
}

func client(r *rt) *http.Client { return &http.Client{Transport: r} }

func TestListObjects_wirePathAndPrefix(t *testing.T) {
	r := &rt{listBody: `{"items":[{"name":"reviews/reviews_com.x_202606.csv"},{"name":"reviews/reviews_com.x_202605.csv"}]}`}
	objs, err := ListObjects(context.Background(), client(r), "pubsite_prod_rev_123", "reviews/reviews_com.x_")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objs) != 2 || objs[0].Name != "reviews/reviews_com.x_202606.csv" {
		t.Fatalf("unexpected objects: %+v", objs)
	}
	call := r.calls[0]
	if !strings.HasPrefix(call, "GET storage.googleapis.com/storage/v1/b/pubsite_prod_rev_123/o?") {
		t.Errorf("list wire path wrong: %s", call)
	}
	if !strings.Contains(call, "prefix=reviews%2Freviews_com.x_") {
		t.Errorf("list did not carry the encoded prefix: %s", call)
	}
}

func TestListObjects_paginates(t *testing.T) {
	// First page returns a token; the client must follow it, then stop.
	r := &pagedRT{}
	objs, err := ListObjects(context.Background(), client2(r), "b", "p")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objs) != 2 || objs[0].Name != "a" || objs[1].Name != "b" {
		t.Fatalf("pagination merged wrong: %+v", objs)
	}
	if r.n != 2 {
		t.Errorf("want 2 list calls, got %d", r.n)
	}
}

type pagedRT struct{ n int }

func (r *pagedRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.n++
	var body string
	if req.URL.Query().Get("pageToken") == "" {
		body = `{"items":[{"name":"a"}],"nextPageToken":"tok"}`
	} else {
		body = `{"items":[{"name":"b"}]}`
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

func client2(r http.RoundTripper) *http.Client { return &http.Client{Transport: r} }

func TestFetchObject_wirePathAltMedia(t *testing.T) {
	r := &rt{getBody: "raw-bytes"}
	got, err := FetchObject(context.Background(), client(r), "bkt", "reviews/reviews_com.x_202606.csv")
	if err != nil {
		t.Fatalf("FetchObject: %v", err)
	}
	if string(got) != "raw-bytes" {
		t.Errorf("body = %q", got)
	}
	call := r.calls[0]
	// The whole object name is one percent-encoded path segment; alt=media set.
	if !strings.Contains(call, "storage.googleapis.com/storage/v1/b/bkt/o/reviews%2Freviews_com.x_202606.csv") {
		t.Errorf("fetch wire path wrong: %s", call)
	}
	if !strings.Contains(call, "alt=media") {
		t.Errorf("fetch missing alt=media: %s", call)
	}
}

func TestErrors_mapToAPIError(t *testing.T) {
	for _, tc := range []struct{ code, wantExit int }{{403, 11}, {404, 30}} {
		r := &rt{code: tc.code, errBody: `{"error":{"code":` + itoa(tc.code) + `,"message":"nope"}}`}
		_, err := FetchObject(context.Background(), client(r), "bkt", "reviews/x.csv")
		var apiErr *api.Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("%d: want *api.Error, got %T", tc.code, err)
		}
		if apiErr.StatusCode != tc.code {
			t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.code)
		}
		if got := apiErr.ExitCode(); got != tc.wantExit {
			t.Errorf("%d → exit %d, want %d", tc.code, got, tc.wantExit)
		}
	}
}

func itoa(n int) string {
	if n == 403 {
		return "403"
	}
	return "404"
}
