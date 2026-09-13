package artifacts_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/artifacts"
)

type rt struct {
	status int
	body   string
	last   *http.Request
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.last = req
	return &http.Response{
		StatusCode: r.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
	}, nil
}

func TestListApks_hitsEditScopedPathAndDecodes(t *testing.T) {
	tr := &rt{status: 200, body: `{"kind":"androidpublisher#apksListResponse","apks":[{"versionCode":42,"binary":{"sha256":"abc"}}]}`}
	lr, raw, err := artifacts.ListApks(context.Background(), &http.Client{Transport: tr}, "com.example.app", "edit-1")
	if err != nil {
		t.Fatalf("ListApks: %v", err)
	}
	if tr.last.Method != http.MethodGet || !strings.HasSuffix(tr.last.URL.Path, "/applications/com.example.app/edits/edit-1/apks") {
		t.Errorf("request = %s %s, want GET .../edits/edit-1/apks", tr.last.Method, tr.last.URL.Path)
	}
	if len(lr.Apks) != 1 || lr.Apks[0].VersionCode != 42 || lr.Apks[0].Binary.Sha256 != "abc" {
		t.Errorf("parsed = %+v", lr)
	}
	if !strings.Contains(string(raw), `"kind"`) {
		t.Errorf("raw body must be verbatim, got %s", raw)
	}
}

func TestListBundles_hitsEditScopedPathAndDecodes(t *testing.T) {
	tr := &rt{status: 200, body: `{"bundles":[{"versionCode":43,"sha256":"def"}]}`}
	lr, _, err := artifacts.ListBundles(context.Background(), &http.Client{Transport: tr}, "com.example.app", "edit-1")
	if err != nil {
		t.Fatalf("ListBundles: %v", err)
	}
	if !strings.HasSuffix(tr.last.URL.Path, "/applications/com.example.app/edits/edit-1/bundles") {
		t.Errorf("path = %s", tr.last.URL.Path)
	}
	if len(lr.Bundles) != 1 || lr.Bundles[0].VersionCode != 43 || lr.Bundles[0].Sha256 != "def" {
		t.Errorf("parsed = %+v", lr)
	}
}

func TestListApks_non2xxMapsToAPIError(t *testing.T) {
	tr := &rt{status: 403, body: `{"error":{"code":403,"message":"denied"}}`}
	_, _, err := artifacts.ListApks(context.Background(), &http.Client{Transport: tr}, "com.example.app", "edit-1")
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 403 || apiErr.Operation != "apks.list" {
		t.Errorf("err = %v, want *api.Error 403 apks.list", err)
	}
}
