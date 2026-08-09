package appstorecatalog_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/appstorecatalog"
)

const eventsBody = `{
  "recentUpdateEvents": [
    {"playAppPackageName": "com.example.one", "eventTime": "2026-07-02T08:00:00Z", "updateType": "MODIFICATION"},
    {"playAppPackageName": "com.example.two", "eventTime": "2026-07-03T09:30:00Z", "updateType": "DELETION"}
  ],
  "nextPageToken": "tok-2"
}`

const (
	startTime = "2026-07-01T00:00:00Z"
	endTime   = "2026-07-08T00:00:00Z"
)

// TestListRecentUpdateEvents_requestShape asserts the GET targets the
// recentUpdateEvents endpoint with the app store package name escaped in the
// path, both required time params in the query, and parses the page.
func TestListRecentUpdateEvents_requestShape(t *testing.T) {
	var got *url.URL
	var gotMethod string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		got, gotMethod = r.URL, r.Method
		return resp(200, eventsBody), nil
	})

	page, raw, err := appstorecatalog.ListRecentUpdateEvents(context.Background(), &http.Client{Transport: rt}, "com.store.alt", startTime, endTime, 0, "")
	if err != nil {
		t.Fatalf("ListRecentUpdateEvents: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if got.Path != "/androidpublisher/v3/appstorecatalog/com.store.alt/recentUpdateEvents" {
		t.Errorf("path = %q, want the recentUpdateEvents endpoint", got.Path)
	}
	q := got.Query()
	if q.Get("startTime") != startTime || q.Get("endTime") != endTime {
		t.Errorf("query = %v, want the required time range", q)
	}
	if q.Has("pageSize") || q.Has("pageToken") {
		t.Errorf("query = %v must omit pageSize/pageToken when unset", q)
	}
	if len(page.RecentUpdateEvents) != 2 {
		t.Fatalf("events = %+v, want two", page.RecentUpdateEvents)
	}
	if page.RecentUpdateEvents[0].UpdateType != appstorecatalog.UpdateTypeModification {
		t.Errorf("first updateType = %q, want MODIFICATION", page.RecentUpdateEvents[0].UpdateType)
	}
	if page.RecentUpdateEvents[1].UpdateType != appstorecatalog.UpdateTypeDeletion {
		t.Errorf("second updateType = %q, want DELETION", page.RecentUpdateEvents[1].UpdateType)
	}
	if page.NextPageToken != "tok-2" {
		t.Errorf("NextPageToken = %q, want tok-2", page.NextPageToken)
	}
	if strings.TrimSpace(string(raw)) != eventsBody {
		t.Errorf("raw = %s, want verbatim pass-through", raw)
	}
}

// TestListRecentUpdateEvents_pagingParams asserts pageSize (>0) and pageToken
// ride the query alongside the unchanged time range.
func TestListRecentUpdateEvents_pagingParams(t *testing.T) {
	var got *url.URL
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL
		return resp(200, `{}`), nil
	})

	if _, _, err := appstorecatalog.ListRecentUpdateEvents(context.Background(), &http.Client{Transport: rt}, "com.store.alt", startTime, endTime, 250, "tok-1"); err != nil {
		t.Fatalf("ListRecentUpdateEvents: %v", err)
	}
	q := got.Query()
	if q.Get("pageSize") != "250" {
		t.Errorf("pageSize = %q, want 250", q.Get("pageSize"))
	}
	if q.Get("pageToken") != "tok-1" {
		t.Errorf("pageToken = %q, want tok-1", q.Get("pageToken"))
	}
	if q.Get("startTime") != startTime || q.Get("endTime") != endTime {
		t.Errorf("query = %v must keep the same time range across pages", q)
	}
}

// TestListRecentUpdateEvents_escapesStorePackage asserts the app store package
// name is escaped in the path, so a value carrying a slash cannot forge a path.
func TestListRecentUpdateEvents_escapesStorePackage(t *testing.T) {
	var gotPath string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.EscapedPath()
		return resp(200, `{}`), nil
	})

	if _, _, err := appstorecatalog.ListRecentUpdateEvents(context.Background(), &http.Client{Transport: rt}, "com.store/../evil", startTime, endTime, 0, ""); err != nil {
		t.Fatalf("ListRecentUpdateEvents: %v", err)
	}
	if !strings.Contains(gotPath, "com.store%2F..%2Fevil") {
		t.Errorf("path %q should carry the escaped app store package name", gotPath)
	}
}

// TestListRecentUpdateEvents_apiError maps a non-2xx to an *api.Error carrying
// the status and the RPC id, so the shared classifier can map it to an exit code.
func TestListRecentUpdateEvents_apiError(t *testing.T) {
	rt := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return resp(404, `{"error":{"message":"not found"}}`), nil
	})

	_, _, err := appstorecatalog.ListRecentUpdateEvents(context.Background(), &http.Client{Transport: rt}, "com.store.alt", startTime, endTime, 0, "")
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error", err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
	if apiErr.Operation != "appstorecatalog.recentupdateevents.list" {
		t.Errorf("Operation = %q, want the recentupdateevents.list RPC id", apiErr.Operation)
	}
}
