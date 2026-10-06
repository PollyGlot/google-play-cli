package releasescmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const options = `{"tracks":[{"displayName":"Production","type":"PRODUCTION","servingReleases":[{"displayName":"1.4.0","versionCodes":["140","141"]}]},{"displayName":"Internal testing","type":"INTERNAL","servingReleases":[{"displayName":"1.5.0-rc1","versionCodes":["150"]}]}]}`

func newRC(t *testing.T, status int, body string) (*kernel.RunContext, *testkit.Fake) {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fake := testkit.NewFake(testkit.Any(status, body))
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: fake})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Scope = token.ReportingScope
	return rc, fake
}

func render(t *testing.T, r output.Renderable, f output.Format) string {
	t.Helper()
	var b bytes.Buffer
	if err := output.Render(&b, f, r.Renderers()); err != nil {
		t.Fatalf("render %s: %v", f, err)
	}
	return b.String()
}

func TestRun_fetchesReleaseFilterOptionsForThePackage(t *testing.T) {
	rc, fake := newRC(t, http.StatusOK, options)
	r, err := Run(rc, Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if c := calls[0]; c.Method != http.MethodGet || c.URL != "https://playdeveloperreporting.googleapis.com/v1beta1/apps/com.example.app:fetchReleaseFilterOptions" {
		t.Errorf("call = %s %s", c.Method, c.URL)
	}
	// WriteJSON indents; verbatim means the same document, every field kept.
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(render(t, r, output.FormatJSON))); err != nil {
		t.Fatalf("JSON output does not parse: %v", err)
	}
	if compact.String() != options {
		t.Errorf("JSON is not the verbatim API body:\n got %s\nwant %s", compact.String(), options)
	}
}

func TestRun_tableShowsTracksReleasesAndVersionCodes(t *testing.T) {
	rc, _ := newRC(t, http.StatusOK, options)
	r, err := Run(rc, Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	table := render(t, r, output.FormatTable)
	for _, want := range []string{"TRACK", "RELEASE", "VERSION_CODES", "Production", "1.4.0", "140, 141", "Internal testing", "1.5.0-rc1", "150"} {
		if !strings.Contains(table, want) {
			t.Errorf("table missing %q:\n%s", want, table)
		}
	}
}

func TestRun_noPackage_isUsageError(t *testing.T) {
	rc, fake := newRC(t, http.StatusOK, options)
	_, err := Run(rc, Input{})
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 2 {
		t.Fatalf("want usage error (exit 2), got %v", err)
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("no API call expected without a package, got %d", len(fake.Calls()))
	}
}

func TestRun_apiDenied_surfacesExit11(t *testing.T) {
	rc, _ := newRC(t, http.StatusForbidden, `{"error":{"code":403,"message":"The caller does not have permission","status":"PERMISSION_DENIED"}}`)
	_, err := Run(rc, Input{Package: "com.example.app"})
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 11 {
		t.Fatalf("want exit 11, got %v", err)
	}
}
