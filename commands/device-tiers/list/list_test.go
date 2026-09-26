package list_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	listcmd "github.com/PollyGlot/google-play-cli/commands/device-tiers/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// newRC wires rt behind a service account and captures stderr.
func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, &stderr
}

const listBody = `{"deviceTierConfigs":[{"deviceTierConfigId":"7"},{"deviceTierConfigId":"8"}],"nextPageToken":"next"}`

// TestRun_pagination_and_passthrough asserts page-size/page-token are sent and
// the response (with nextPageToken) is passed through verbatim.
func TestRun_pagination_and_passthrough(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, listBody))
	rc, _ := newRC(t, fake)
	r, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", PageSize: 25, PageToken: "tok"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	gotURL := fake.Calls()[0].URL
	for _, want := range []string{"pageSize=25", "pageToken=tok"} {
		if !strings.Contains(gotURL, want) {
			t.Errorf("url %q missing %q", gotURL, want)
		}
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"nextPageToken":"next"`) {
		t.Errorf("json %s should preserve nextPageToken", out.String())
	}
}

// TestRun_nextPageTokenNote asserts a page carrying nextPageToken tells the
// operator on stderr how to fetch the next one: a first page in table or
// markdown would otherwise read as the whole list.
func TestRun_nextPageTokenNote(t *testing.T) {
	rc, stderr := newRC(t, testkit.NewFake(testkit.Any(http.StatusOK, listBody)))
	if _, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "--page-token next") {
		t.Errorf("stderr %q should carry the next --page-token note", stderr.String())
	}

	rc, stderr = newRC(t, testkit.NewFake(testkit.Any(http.StatusOK, `{"deviceTierConfigs":[{"deviceTierConfigId":"7"}]}`)))
	if _, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("the last page must not claim more: stderr %q", stderr.String())
	}
}
