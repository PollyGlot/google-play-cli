package artifacts_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/artifacts"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// onlyCall returns the single request the Fake served, failing the test on
// any other count.
func onlyCall(t *testing.T, f *testkit.Fake) testkit.Call {
	t.Helper()
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("want exactly one request, got %d: %+v", len(calls), calls)
	}
	return calls[0]
}

func TestListApks_hitsEditScopedPathAndDecodes(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(200, `{"kind":"androidpublisher#apksListResponse","apks":[{"versionCode":42,"binary":{"sha256":"abc"}}]}`))
	lr, raw, err := artifacts.ListApks(context.Background(), &http.Client{Transport: fake}, "com.example.app", "edit-1")
	if err != nil {
		t.Fatalf("ListApks: %v", err)
	}
	if c := onlyCall(t, fake); c.Method != http.MethodGet || !strings.HasSuffix(c.Path, "/applications/com.example.app/edits/edit-1/apks") {
		t.Errorf("request = %s %s, want GET .../edits/edit-1/apks", c.Method, c.Path)
	}
	if len(lr.Apks) != 1 || lr.Apks[0].VersionCode != 42 || lr.Apks[0].Binary.Sha256 != "abc" {
		t.Errorf("parsed = %+v", lr)
	}
	if !strings.Contains(string(raw), `"kind"`) {
		t.Errorf("raw body must be verbatim, got %s", raw)
	}
}

func TestListBundles_hitsEditScopedPathAndDecodes(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(200, `{"bundles":[{"versionCode":43,"sha256":"def"}]}`))
	lr, _, err := artifacts.ListBundles(context.Background(), &http.Client{Transport: fake}, "com.example.app", "edit-1")
	if err != nil {
		t.Fatalf("ListBundles: %v", err)
	}
	if c := onlyCall(t, fake); !strings.HasSuffix(c.Path, "/applications/com.example.app/edits/edit-1/bundles") {
		t.Errorf("path = %s", c.Path)
	}
	if len(lr.Bundles) != 1 || lr.Bundles[0].VersionCode != 43 || lr.Bundles[0].Sha256 != "def" {
		t.Errorf("parsed = %+v", lr)
	}
}

func TestListApks_non2xxMapsToAPIError(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(403, `{"error":{"code":403,"message":"denied"}}`))
	_, _, err := artifacts.ListApks(context.Background(), &http.Client{Transport: fake}, "com.example.app", "edit-1")
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 403 || apiErr.Operation != "apks.list" {
		t.Errorf("err = %v, want *api.Error 403 apks.list", err)
	}
}
