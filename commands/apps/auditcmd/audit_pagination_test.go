package auditcmd_test

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/apps/auditcmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// discoveryRC wires rt behind a service account, like the sweep tests do.
func discoveryRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// searchOnly answers apps.search with page(n) for the n-th call and refuses
// anything else: discovery must fail before a single app is read.
func searchOnly(page func(n int) string) (*testkit.Fake, *atomic.Int64) {
	var n atomic.Int64
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/apps:search") {
			return http.StatusOK, page(int(n.Add(1))), true
		}
		return 0, "", false
	}), &n
}

// A server that hands back the token it was just given would page forever:
// discovery refuses the repeat instead of auditing until the quota runs out.
func TestRun_discoveryRefusesARepeatedPageToken(t *testing.T) {
	fake, calls := searchOnly(func(int) string {
		return `{"apps":[{"packageName":"com.example.a"}],"nextPageToken":"same"}`
	})
	_, err := auditcmd.Run(discoveryRC(t, fake), auditcmd.Input{})
	if err == nil || !strings.Contains(err.Error(), "pagination token loop detected in apps.search") {
		t.Fatalf("err = %v, want the repeated-token refusal", err)
	}
	if !strings.Contains(err.Error(), "name the packages as arguments") {
		t.Errorf("err = %v, want the named-packages escape hatch", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("apps.search sent %d times, want 2 (the repeat is caught on its first return)", got)
	}
	if fake.Wrote() {
		t.Error("a failed discovery must not open any Edit")
	}
}

// Past the page bound with a token still set, the account list is partial:
// the audit fails loudly instead of sweeping a subset as if it were the whole.
func TestRun_discoveryFailsAtThePageBound(t *testing.T) {
	fake, calls := searchOnly(func(n int) string {
		return `{"apps":[{"packageName":"com.example.a` + strconv.Itoa(n) + `"}],"nextPageToken":"t` + strconv.Itoa(n) + `"}`
	})
	_, err := auditcmd.Run(discoveryRC(t, fake), auditcmd.Input{})
	if err == nil || !strings.Contains(err.Error(), "apps.search still had pages after 1000 requests") {
		t.Fatalf("err = %v, want the page-bound refusal", err)
	}
	if got := calls.Load(); got != 1000 {
		t.Errorf("apps.search sent %d times, want exactly 1000", got)
	}
	if code := exit.For(err); code != 50 {
		t.Errorf("exit = %d, want 50", code)
	}
}
