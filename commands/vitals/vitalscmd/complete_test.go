package vitalscmd

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const releaseOptions = `{"tracks":[
 {"displayName":"Production","type":"PRODUCTION","servingReleases":[{"displayName":"1.4.0","versionCodes":["140","141"]}]},
 {"displayName":"Internal testing","type":"INTERNAL","servingReleases":[{"displayName":"1.5.0-rc1","versionCodes":["150"]},{"displayName":"1.4.0","versionCodes":["141"]}]}]}`

// complete drives cobra's own `__complete` protocol (what the generated shell
// scripts call) for `--version-code <toComplete>` on the crashes preset, with
// rt as the only network. It returns the suggestion lines, the directive line
// and whatever reached stderr.
func complete(t *testing.T, rt http.RoundTripper, toComplete string) (suggestions []string, directive, stderr string) {
	t.Helper()
	// The machine's own Account selection must not leak into the test.
	t.Setenv(resolver.EnvAccount, "")
	dir := t.TempDir()
	boot := kernel.Boot{ConfigPath: filepath.Join(dir, "config.json"), KeystoreRoot: filepath.Join(dir, "accounts")}
	root := &cobra.Command{Use: "gplay"}
	root.AddCommand(kernel.WithScope(NewPresetCommand(boot, Presets[0]), token.ReportingScope))
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{cobra.ShellCompRequestCmd, "crashes", "--package", "com.example.app", "--version-code", toComplete})
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatalf("__complete: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	return lines[:len(lines)-1], lines[len(lines)-1], errb.String()
}

// wantNoFileDirective is ShellCompDirectiveNoFileComp as cobra prints it: a
// version code is never a path, so even an empty answer must not fall back
// to file names. ShellCompDirectiveError (":1") would be the error signal.
var wantNoFileDirective = ":" + "4"

func TestCompleteVersionCodes_suggestsTheCodesPlayHasVitalsFor(t *testing.T) {
	t.Setenv(resolver.EnvServiceAccount, string(testkit.ServiceAccountJSON(t)))
	fake := testkit.NewFake(testkit.Any(http.StatusOK, releaseOptions))

	got, directive, _ := complete(t, fake, "")

	// Newest first, each code once (141 is served by two tracks), described by
	// the release that carries it.
	want := []string{"150\t1.5.0-rc1 (Internal testing)", "141\t1.4.0 (Production)", "140\t1.4.0 (Production)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("suggestions =\n %q\nwant\n %q", got, want)
	}
	if directive != wantNoFileDirective {
		t.Errorf("directive = %q, want %q", directive, wantNoFileDirective)
	}
	calls := fake.Calls()
	if len(calls) != 1 || !strings.HasSuffix(calls[0].URL, "/apps/com.example.app:fetchReleaseFilterOptions") {
		t.Errorf("calls = %+v, want one fetchReleaseFilterOptions for the --package", calls)
	}
}

func TestCompleteVersionCodes_filtersOnWhatIsTyped(t *testing.T) {
	t.Setenv(resolver.EnvServiceAccount, string(testkit.ServiceAccountJSON(t)))
	got, _, _ := complete(t, testkit.NewFake(testkit.Any(http.StatusOK, releaseOptions)), "14")
	want := []string{"141\t1.4.0 (Production)", "140\t1.4.0 (Production)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("suggestions = %q, want %q", got, want)
	}
}

// A completion runs on every TAB, without the user asking for a call: any
// failure (no credential, an API refusal, a slow network) yields no
// suggestion, no error directive and nothing on stderr.
func TestCompleteVersionCodes_isSilentOnFailure(t *testing.T) {
	cases := []struct {
		name string
		sa   bool
		rt   http.RoundTripper
	}{
		{"no credential", false, testkit.NewFake(testkit.Refuse(t, " without a credential"))},
		{"API denied", true, testkit.NewFake(testkit.Any(http.StatusForbidden, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`))},
		{"malformed answer", true, testkit.NewFake(testkit.Any(http.StatusOK, `not json`))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(resolver.EnvServiceAccount, "")
			if tc.sa {
				t.Setenv(resolver.EnvServiceAccount, string(testkit.ServiceAccountJSON(t)))
			}
			got, directive, stderr := complete(t, tc.rt, "")
			if len(got) != 0 {
				t.Errorf("suggestions = %q, want none", got)
			}
			if directive != wantNoFileDirective {
				t.Errorf("directive = %q, want %q (no error directive)", directive, wantNoFileDirective)
			}
			if strings.Contains(stderr, "gplay") || strings.Contains(stderr, "warning") || strings.Contains(stderr, "error:") {
				t.Errorf("completion leaked to stderr: %q", stderr)
			}
		})
	}
}

func TestCompleteVersionCodes_givesUpAfterAShortTimeout(t *testing.T) {
	t.Setenv(resolver.EnvServiceAccount, string(testkit.ServiceAccountJSON(t)))
	defer func(d time.Duration) { completionTimeout = d }(completionTimeout)
	completionTimeout = 50 * time.Millisecond
	hang := testkit.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := testkit.TokenResponse(r); ok {
			return resp, nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})

	start := time.Now()
	got, directive, _ := complete(t, hang, "")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("completion took %s, want it bounded by the completion timeout", elapsed)
	}
	if len(got) != 0 || directive != wantNoFileDirective {
		t.Errorf("suggestions = %q directive = %q, want none and %q", got, directive, wantNoFileDirective)
	}
}
