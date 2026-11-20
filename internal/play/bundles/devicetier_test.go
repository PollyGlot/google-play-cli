package bundles_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/bundles"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func initiateQuery(t *testing.T, o bundles.Options) url.Values {
	t.Helper()
	aab := filepath.Join(t.TempDir(), "app.aab")
	if err := os.WriteFile(aab, []byte("aab"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := testkit.NewFake(testkit.Any(http.StatusBadRequest, `{"error":{"code":400,"message":"stop"}}`))
	hc := &http.Client{Transport: fake}

	if _, err := bundles.UploadWith(context.Background(), hc, "com.example.app", "edit-1", aab, o); err == nil {
		t.Fatal("UploadWith succeeded against a refused initiate")
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Method != http.MethodPost {
		t.Fatalf("want one POST initiate, got %+v", calls)
	}
	q, err := url.ParseQuery(calls[0].Query)
	if err != nil {
		t.Fatalf("parse initiate query %q: %v", calls[0].Query, err)
	}
	return q
}

func TestUploadWith_deviceTierConfig_sentOnInitiate(t *testing.T) {
	q := initiateQuery(t, bundles.Options{DeviceTierConfigID: "LATEST"})
	if got := q.Get("deviceTierConfigId"); got != "LATEST" {
		t.Errorf("deviceTierConfigId = %q, want LATEST (query %v)", got, q)
	}
	if got := q.Get("uploadType"); got != "resumable" {
		t.Errorf("uploadType = %q, want resumable", got)
	}
}

func TestUploadWith_zeroOptions_noDeviceTierConfig(t *testing.T) {
	q := initiateQuery(t, bundles.Options{})
	if _, ok := q["deviceTierConfigId"]; ok {
		t.Errorf("plain upload sent deviceTierConfigId: %v", q)
	}
	if got := q.Get("uploadType"); got != "resumable" {
		t.Errorf("uploadType = %q, want resumable", got)
	}
}
