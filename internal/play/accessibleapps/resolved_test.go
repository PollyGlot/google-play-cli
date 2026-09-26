// Migration proof for #518: accessibleapps now takes its verb and URL from
// internal/apiregistry instead of local literals. accessibleapps_test.go is
// untouched; what is added here is the ABSOLUTE URL, which matters more here
// than elsewhere because this is the only migrated method served by the Play
// Developer Reporting host rather than androidpublisher.
package accessibleapps_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/accessibleapps"
)

// pinned answers {} to every call and records what was sent.
func pinned() *testkit.Fake { return testkit.NewFake(testkit.Any(http.StatusOK, `{}`)) }

// first is the first call f recorded, or the zero Call when none was sent.
func first(f *testkit.Fake) testkit.Call {
	if calls := f.Calls(); len(calls) > 0 {
		return calls[0]
	}
	return testkit.Call{}
}

func TestResolvedURLUnchanged(t *testing.T) {
	const base = "https://playdeveloperreporting.googleapis.com/v1beta1/apps:search"

	for _, tc := range []struct {
		name      string
		pageSize  int
		pageToken string
		want      string
	}{
		{name: "no parameters", want: base},
		{name: "page size and token", pageSize: 10, pageToken: "t2", want: base + "?pageSize=10&pageToken=t2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := pinned()
			if _, _, err := accessibleapps.Search(context.Background(), &http.Client{Transport: rt}, tc.pageSize, tc.pageToken); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if first(rt).URL != tc.want {
				t.Errorf("URL = %q, want %q", first(rt).URL, tc.want)
			}
			if first(rt).Method != http.MethodGet {
				t.Errorf("verb = %q, want GET", first(rt).Method)
			}
		})
	}
}
