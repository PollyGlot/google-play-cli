package gcs

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// bucketFake serves canned 200 responses keyed by whether the path is a list
// (…/o) or a media fetch (…/o/…).
func bucketFake(listBody, getBody string) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if strings.Contains(c.Path, "/o/") {
			return http.StatusOK, getBody, true
		}
		return http.StatusOK, listBody, true
	})
}

func client(r http.RoundTripper) *http.Client { return &http.Client{Transport: r} }

// wire renders a call as METHOD host/escaped-path?query, the form the path and
// escaping assertions read.
func wire(c testkit.Call) string {
	return c.Method + " " + c.Host + strings.TrimPrefix(c.URL, "https://"+c.Host)
}

func TestListObjects_wirePathAndPrefix(t *testing.T) {
	r := bucketFake(`{"items":[{"name":"reviews/reviews_com.x_202606.csv"},{"name":"reviews/reviews_com.x_202605.csv"}]}`, "")
	objs, err := ListObjects(context.Background(), client(r), "pubsite_prod_rev_123", "reviews/reviews_com.x_")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objs) != 2 || objs[0].Name != "reviews/reviews_com.x_202606.csv" {
		t.Fatalf("unexpected objects: %+v", objs)
	}
	call := wire(r.Calls()[0])
	if !strings.HasPrefix(call, "GET storage.googleapis.com/storage/v1/b/pubsite_prod_rev_123/o?") {
		t.Errorf("list wire path wrong: %s", call)
	}
	if !strings.Contains(call, "prefix=reviews%2Freviews_com.x_") {
		t.Errorf("list did not carry the encoded prefix: %s", call)
	}
}

func TestListObjects_paginates(t *testing.T) {
	// First page returns a token; the client must follow it, then stop.
	r := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		q, _ := url.ParseQuery(c.Query)
		if q.Get("pageToken") == "" {
			return http.StatusOK, `{"items":[{"name":"a"}],"nextPageToken":"tok"}`, true
		}
		return http.StatusOK, `{"items":[{"name":"b"}]}`, true
	})
	objs, err := ListObjects(context.Background(), client(r), "b", "p")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objs) != 2 || objs[0].Name != "a" || objs[1].Name != "b" {
		t.Fatalf("pagination merged wrong: %+v", objs)
	}
	if n := len(r.Calls()); n != 2 {
		t.Errorf("want 2 list calls, got %d", n)
	}
}

func TestFetchObject_wirePathAltMedia(t *testing.T) {
	r := bucketFake("", "raw-bytes")
	got, err := FetchObject(context.Background(), client(r), "bkt", "reviews/reviews_com.x_202606.csv")
	if err != nil {
		t.Fatalf("FetchObject: %v", err)
	}
	if string(got) != "raw-bytes" {
		t.Errorf("body = %q", got)
	}
	call := wire(r.Calls()[0])
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
		r := testkit.NewFake(testkit.Any(tc.code, `{"error":{"code":`+itoa(tc.code)+`,"message":"nope"}}`))
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

func TestListObjects_carriesSizeAndUpdated(t *testing.T) {
	r := bucketFake(`{"items":[{"name":"stats/installs/installs_com.x_202609_country.csv","size":"20480","updated":"2026-10-04T08:00:00.000Z","md5Hash":"x"}]}`, "")
	objs, err := ListObjects(context.Background(), client(r), "b", "stats/installs/")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objs) != 1 || objs[0].Size != "20480" || objs[0].Updated != "2026-10-04T08:00:00.000Z" {
		t.Fatalf("metadata lost: %+v", objs)
	}
}

func TestFetchObjectLimit_callerChosenCap(t *testing.T) {
	r := bucketFake("", "0123456789")
	if got, err := FetchObjectLimit(context.Background(), client(r), "b", "o.csv", 10); err != nil || string(got) != "0123456789" {
		t.Fatalf("at the cap: %q, %v", got, err)
	}
	_, err := FetchObjectLimit(context.Background(), client(r), "b", "o.csv", 9)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "exceeds") || !strings.Contains(apiErr.Message, "9 bytes") {
		t.Fatalf("over the cap: err = %v, want a size-limit *api.Error naming the cap", err)
	}
	if !errors.Is(err, ErrTooLarge) || apiErr.ExitCode() != 30 {
		t.Errorf("over the cap: errors.Is(ErrTooLarge) = %v, exit %d; want true and the unchanged 30", errors.Is(err, ErrTooLarge), apiErr.ExitCode())
	}
}
