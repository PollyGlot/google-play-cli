package view_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	viewcmd "github.com/PollyGlot/google-play-cli/commands/device-tiers/view"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

const configBody = `{"deviceTierConfigId":"42","deviceGroups":[{"name":"high"}]}`

// TestRun_happyPath_addressesIDAndPassesThrough asserts the GET addresses the id
// and the JSON view is verbatim.
func TestRun_happyPath_addressesIDAndPassesThrough(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, configBody))
	r, err := viewcmd.Run(newRC(t, fake), viewcmd.Input{Package: "com.example.app", ID: "42"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotURL := fake.Calls()[0].URL; !strings.HasSuffix(gotURL, "/deviceTierConfigs/42") {
		t.Errorf("url %q should address config 42", gotURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.TrimSpace(out.String()) != configBody {
		t.Errorf("json = %s, want verbatim", out.String())
	}
}

// TestRun_missingID_exit2 asserts an empty id is CLI misuse, refused before
// any API call (the Fake fails an unclaimed call, and none is claimed).
func TestRun_missingID_exit2(t *testing.T) {
	fake := testkit.NewFake()
	_, err := viewcmd.Run(newRC(t, fake), viewcmd.Input{Package: "com.example.app"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("unexpected call: %s", calls[0].URL)
	}
}
