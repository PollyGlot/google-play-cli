package query

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func queryDimensions(t *testing.T, fake *testkit.Fake) []string {
	t.Helper()
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("no API call recorded")
	}
	var body struct {
		Dimensions []string `json:"dimensions"`
	}
	if err := json.Unmarshal(calls[len(calls)-1].Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body.Dimensions
}

func TestRun_slowstartrate_requiredDimension(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dimensions []string
		want       string
	}{
		{"added when --dimensions is absent", nil, "startType"},
		{"added after the asked dimensions", []string{"versionCode"}, "versionCode,startType"},
		{"accepted and not repeated", []string{"startType", "countryCode"}, "startType,countryCode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := testkit.NewFake(testkit.Any(http.StatusOK, `{"rows":[]}`))
			rc, _, _ := newRC(t, fake)
			if _, err := Run(rc, Input{MetricSet: "slowstartrate", Package: "com.example.app", Dimensions: tc.dimensions}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := strings.Join(queryDimensions(t, fake), ","); got != tc.want {
				t.Errorf("dimensions = %q, want %q", got, tc.want)
			}
		})
	}
}
