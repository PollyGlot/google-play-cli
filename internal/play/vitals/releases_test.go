package vitals_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// releaseOptions is a fetchReleaseFilterOptions answer shaped like the
// Discovery schema: tracks → servingReleases → versionCodes (int64 as strings).
const releaseOptions = `{"tracks":[
 {"displayName":"Production","type":"PRODUCTION","servingReleases":[
  {"displayName":"1.4.0","versionCodes":["140","141"]}]},
 {"displayName":"Internal testing","type":"INTERNAL","servingReleases":[
  {"displayName":"1.5.0-rc1","versionCodes":["150"]},
  {"displayName":"1.4.0","versionCodes":["141"]}]},
 {"displayName":"Closed testing","type":"CLOSED"}]}`

// TestFetchReleaseFilterOptions_GETsTheAppsCustomMethod pins the resolved
// endpoint: a GET on the app's `:fetchReleaseFilterOptions` custom method on
// the Reporting host, and the body passed through untouched (ADR-0003).
func TestFetchReleaseFilterOptions_GETsTheAppsCustomMethod(t *testing.T) {
	rt := testkit.NewFake(testkit.Any(http.StatusOK, releaseOptions))
	raw, err := vitals.FetchReleaseFilterOptions(context.Background(), &http.Client{Transport: rt}, "com.example.app")
	if err != nil {
		t.Fatalf("FetchReleaseFilterOptions: %v", err)
	}
	c := lastCall(t, rt)
	if want := reportingRoot + ":fetchReleaseFilterOptions"; c.URL != want {
		t.Errorf("URL = %q, want %q", c.URL, want)
	}
	if c.Method != http.MethodGet {
		t.Errorf("verb = %q, want GET", c.Method)
	}
	if string(raw) != releaseOptions {
		t.Errorf("body not verbatim:\n got %s\nwant %s", raw, releaseOptions)
	}
}

// TestParseReleases_oneRowPerServingRelease flattens tracks → releases into
// rows, keeping API order; a track serving nothing still gets a row so the
// table shows it exists.
func TestParseReleases_oneRowPerServingRelease(t *testing.T) {
	got, err := vitals.ParseReleases([]byte(releaseOptions))
	if err != nil {
		t.Fatalf("ParseReleases: %v", err)
	}
	want := []vitals.Release{
		{Track: "Production", TrackType: "PRODUCTION", Release: "1.4.0", VersionCodes: []string{"140", "141"}},
		{Track: "Internal testing", TrackType: "INTERNAL", Release: "1.5.0-rc1", VersionCodes: []string{"150"}},
		{Track: "Internal testing", TrackType: "INTERNAL", Release: "1.4.0", VersionCodes: []string{"141"}},
		{Track: "Closed testing", TrackType: "CLOSED"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows =\n %+v\nwant\n %+v", got, want)
	}
}
