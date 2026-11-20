package output_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/appsigning"
	"github.com/PollyGlot/google-play-cli/internal/play/customapps"
	"github.com/PollyGlot/google-play-cli/internal/play/games"
	"github.com/PollyGlot/google-play-cli/internal/play/gcs"
	"github.com/PollyGlot/google-play-cli/internal/play/team"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const denied = `{"error":{"code":403,"message":"The caller does not have permission","status":"PERMISSION_DENIED"}}`

func deniedClient() *http.Client {
	return &http.Client{Transport: testkit.NewFake(testkit.Any(http.StatusForbidden, denied))}
}

func TestWriteErrorEnvelope_resourceOnEveryAxis(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(artifact, []byte("PK\x03\x04"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		golden string
		call   func(ctx context.Context, hc *http.Client) error
		kind   api.ResourceKind
		id     string
	}{
		{
			name: "team", golden: "envelope_resource_team.golden",
			call: func(ctx context.Context, hc *http.Client) error {
				_, _, err := team.ListUsers(ctx, hc, "1234567890123456789")
				return err
			},
			kind: api.KindDeveloperAccount, id: "1234567890123456789",
		},
		{
			name: "games", golden: "envelope_resource_games.golden",
			call: func(ctx context.Context, hc *http.Client) error {
				_, _, err := games.ListAchievements(ctx, hc, "123456789012", 0, "")
				return err
			},
			kind: api.KindGamesApplication, id: "123456789012",
		},
		{
			name: "gcs", golden: "envelope_resource_bucket.golden",
			call: func(ctx context.Context, hc *http.Client) error {
				_, err := gcs.FetchObject(ctx, hc, "pubsite_prod_rev_01234567890987654321", "reviews/reviews_com.example.app_202609.csv")
				return err
			},
			kind: api.KindBucket, id: "pubsite_prod_rev_01234567890987654321",
		},
		{
			name: "games achievement",
			call: func(ctx context.Context, hc *http.Client) error {
				_, _, err := games.GetAchievement(ctx, hc, "CgkI0123456789EAIQAQ")
				return err
			},
			kind: api.KindAchievement, id: "CgkI0123456789EAIQAQ",
		},
		{
			name: "games leaderboard",
			call: func(ctx context.Context, hc *http.Client) error {
				_, _, err := games.GetLeaderboard(ctx, hc, "CgkI0123456789EAIQAg")
				return err
			},
			kind: api.KindLeaderboard, id: "CgkI0123456789EAIQAg",
		},
		{
			name: "custom app",
			call: func(ctx context.Context, hc *http.Client) error {
				_, _, err := customapps.Create(ctx, hc, "1234567890123456789", artifact, customapps.CreateOpts{Title: "App", LanguageCode: "en-US"})
				return err
			},
			kind: api.KindDeveloperAccount, id: "1234567890123456789",
		},
		{
			name: "app signing by app ID",
			call: func(ctx context.Context, hc *http.Client) error {
				_, _, err := appsigning.Enroll(ctx, hc, "4974155822911957000", appsigning.EnrollOpts{KmsKeyResource: "k"})
				return err
			},
			kind: api.KindApp, id: "4974155822911957000",
		},
		{
			name: "app signing by package",
			call: func(ctx context.Context, hc *http.Client) error {
				_, _, err := appsigning.Enroll(ctx, hc, "com.example.app", appsigning.EnrollOpts{KmsKeyResource: "k"})
				return err
			},
			kind: api.KindPackage, id: "com.example.app",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(t.Context(), deniedClient())
			if err == nil {
				t.Fatal("a 403 must fail the call")
			}
			if tc.golden != "" {
				outputtest.GoldenJSON(t, tc.golden, jsonOnly(func(w io.Writer) error {
					return output.WriteErrorEnvelope(w, err)
				}))
			}
			var buf bytes.Buffer
			if werr := output.WriteErrorEnvelope(&buf, err); werr != nil {
				t.Fatalf("WriteErrorEnvelope: %v", werr)
			}
			env := decodeEnvelope(t, &buf)
			r := env.Error.Resource
			if r == nil || r.Kind != string(tc.kind) || r.ID != tc.id {
				t.Fatalf("resource = %+v, want {%s %s}", r, tc.kind, tc.id)
			}
			wantPkg := ""
			if tc.kind == api.KindPackage {
				wantPkg = tc.id
			}
			if env.Error.Package != wantPkg {
				t.Errorf("package = %q, want %q: package carries real package names only", env.Error.Package, wantPkg)
			}
		})
	}
}
