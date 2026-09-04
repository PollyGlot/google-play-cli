// Migration proof for #518: accessibleapps now takes its verb and URL from
// internal/apiregistry instead of local literals. accessibleapps_test.go is
// untouched; what is added here is the ABSOLUTE URL, which matters more here
// than elsewhere because this is the only migrated method served by the Play
// Developer Reporting host rather than androidpublisher.
package accessibleapps_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/accessibleapps"
)

type pinRT struct {
	url, verb string
}

func (r *pinRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.url == "" {
		r.url, r.verb = req.URL.String(), req.Method
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}, nil
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
			rt := &pinRT{}
			if _, _, err := accessibleapps.Search(context.Background(), &http.Client{Transport: rt}, tc.pageSize, tc.pageToken); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if rt.url != tc.want {
				t.Errorf("URL = %q, want %q", rt.url, tc.want)
			}
			if rt.verb != http.MethodGet {
				t.Errorf("verb = %q, want GET", rt.verb)
			}
		})
	}
}
