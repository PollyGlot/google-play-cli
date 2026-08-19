package discovery_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/discovery"
)

// TestNormalize_sortsStripsEtagAndIsIdempotent feeds a deliberately
// un-normalized document: keys out of order, a transport "etag", an HTML char
// in a description, and asserts Normalize sorts keys, drops "etag", and is
// idempotent. Idempotence is the property the offline integrity gate leans on.
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
	// Top-level keys must be sorted: description < id < resources < revision.
	if got := keyOrder(s); !isSorted(got) {
		t.Errorf("top-level keys not sorted: %v", got)
	}
	// HTML must not be escaped (readable descriptions).
	if !strings.Contains(s, "<Edit>") {
		t.Errorf("HTML was escaped, want literal <Edit>:\n%s", s)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Error("normalized output must end with a trailing newline")
	}

	// Idempotent: re-normalizing yields byte-identical output.
	again, err := discovery.Normalize(out)
	if err != nil {
		t.Fatalf("re-Normalize: %v", err)
	}
	if !bytes.Equal(out, again) {
		t.Errorf("Normalize not idempotent:\nfirst:\n%s\nsecond:\n%s", out, again)
	}
}

// TestRenderPaths_walksNestedResourcesSortedByID asserts RenderPaths recurses
// into nested resources, emits one tab-separated line per method, and sorts the
// whole index by method id.
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

// TestFetch_hitsDiscoveryURLAndReturnsBody exercises Fetch against a loopback
// httptest server (no outbound network, per CLAUDE.md).
func TestFetch_hitsDiscoveryURLAndReturnsBody(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"revision":"1"}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	// DiscoveryURL hardcodes https; test the request shape via a custom client
	// that rewrites the scheme/host to the loopback server.
	svc := discovery.Service{Name: "androidpublisher", Host: host, Version: "v3"}
	client := &http.Client{Transport: schemeRewriter{base: srv.URL}}

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
	client := &http.Client{Transport: schemeRewriter{base: srv.URL}}
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

// schemeRewriter sends every request to base (the loopback server) regardless
// of the request URL's host, so Fetch's https://<host>/... URL is exercised
// without real DNS or TLS.
type schemeRewriter struct{ base string }

func (s schemeRewriter) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = "http"
	r.URL.Host = strings.TrimPrefix(s.base, "http://")
	return http.DefaultTransport.RoundTrip(r)
}

func keyOrder(jsonObj string) []string {
	var keys []string
	depth := 0
	for _, line := range strings.Split(jsonObj, "\n") {
		// count indentation: top-level keys sit at exactly two spaces.
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
