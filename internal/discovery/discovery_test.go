package discovery_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/discovery"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestNormalize_sortsStripsEtagAndIsIdempotent(t *testing.T) {
	raw := []byte(`{
	  "revision": "20260608",
	  "etag": "\"abc123\"",
	  "resources": {"edits": {"methods": {"commit": {"id": "x", "httpMethod": "POST", "path": "p"}}}},
	  "description": "insert an <Edit> then commit",
	  "id": "androidpublisher:v3"
	}`)

	out, err := discovery.Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	s := string(out)
	if strings.Contains(s, "etag") {
		t.Errorf("etag not stripped:\n%s", s)
	}
	if got := keyOrder(s); !isSorted(got) {
		t.Errorf("top-level keys not sorted: %v", got)
	}
	if !strings.Contains(s, "<Edit>") {
		t.Errorf("HTML was escaped, want literal <Edit>:\n%s", s)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Error("normalized output must end with a trailing newline")
	}

	again, err := discovery.Normalize(out)
	if err != nil {
		t.Fatalf("re-Normalize: %v", err)
	}
	if !bytes.Equal(out, again) {
		t.Errorf("Normalize not idempotent:\nfirst:\n%s\nsecond:\n%s", out, again)
	}
}

func TestRenderPaths_walksNestedResourcesSortedByID(t *testing.T) {
	snap := []byte(`{
	  "resources": {
	    "edits": {
	      "methods": {
	        "commit": {"id": "androidpublisher.edits.commit", "httpMethod": "POST", "path": "edits/{editId}:commit"}
	      },
	      "resources": {
	        "tracks": {
	          "methods": {
	            "update": {"id": "androidpublisher.edits.tracks.update", "httpMethod": "PUT", "path": "edits/{editId}/tracks/{track}"}
	          }
	        }
	      }
	    },
	    "reviews": {
	      "methods": {
	        "list": {"id": "androidpublisher.reviews.list", "httpMethod": "GET", "path": "reviews"}
	      }
	    }
	  }
	}`)

	out, err := discovery.RenderPaths([][]byte{snap})
	if err != nil {
		t.Fatalf("RenderPaths: %v", err)
	}

	want := "androidpublisher.edits.commit\tPOST\tedits/{editId}:commit\n" +
		"androidpublisher.edits.tracks.update\tPUT\tedits/{editId}/tracks/{track}\n" +
		"androidpublisher.reviews.list\tGET\treviews\n"
	if string(out) != want {
		t.Errorf("RenderPaths =\n%q\nwant\n%q", out, want)
	}
}

func TestFetch_hitsDiscoveryURLAndReturnsBody(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"revision":"1"}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	svc := discovery.Service{Name: "androidpublisher", Host: host, Version: "v3"}
	client := &http.Client{Transport: schemeRewriter(srv.URL)}

	body, err := discovery.Fetch(context.Background(), client, svc)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != `{"revision":"1"}` {
		t.Errorf("body = %q", body)
	}
	if gotPath != "/$discovery/rest" {
		t.Errorf("path = %q, want /$discovery/rest", gotPath)
	}
	if gotQuery != "version=v3" {
		t.Errorf("query = %q, want version=v3", gotQuery)
	}
}

func TestFetch_nonOKStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	svc := discovery.Service{Name: "x", Host: strings.TrimPrefix(srv.URL, "http://"), Version: "v3"}
	client := &http.Client{Transport: schemeRewriter(srv.URL)}
	if _, err := discovery.Fetch(context.Background(), client, svc); err == nil {
		t.Error("Fetch: want error on HTTP 404, got nil")
	}
}

func TestSnapshotFilename(t *testing.T) {
	svc := discovery.Service{Name: "androidpublisher", Version: "v3"}
	if got := svc.SnapshotFilename(); got != "androidpublisher_v3.json" {
		t.Errorf("SnapshotFilename = %q", got)
	}
}

func schemeRewriter(base string) http.RoundTripper {
	return testkit.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(base, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
}

func keyOrder(jsonObj string) []string {
	var keys []string
	depth := 0
	for _, line := range strings.Split(jsonObj, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		_ = depth
		if indent == 2 && strings.HasPrefix(trimmed, `"`) {
			if i := strings.Index(trimmed[1:], `"`); i >= 0 {
				keys = append(keys, trimmed[1:1+i])
			}
		}
	}
	return keys
}

func isSorted(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}
