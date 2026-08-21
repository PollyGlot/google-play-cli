package recovery_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

const draftBody = `{"appRecoveryId":"555","status":"RECOVERY_STATUS_DRAFT","createTime":"2026-06-18T00:00:00Z"}`

// TestCreate_buildsTargeting_noEdit asserts the draft POST carries the version +
// user targeting and hits the appRecoveries collection (no /edits/).
func TestCreate_buildsTargeting_noEdit(t *testing.T) {
	var gotURL string
	var gotBody []byte
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotBody, _ = io.ReadAll(r.Body)
		return resp(200, draftBody), nil
	})
	hc := &http.Client{Transport: rt}

	a, raw, err := recovery.Create(context.Background(), hc, "com.example.app", recovery.CreateOpts{
		VersionCodes: []int64{142}, Regions: []string{"US", "FR"}, SdkLevels: []int64{29, 30}, RemoteInAppUpdate: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasSuffix(gotURL, "/applications/com.example.app/appRecoveries") || strings.Contains(gotURL, "/edits/") {
		t.Errorf("url %q should be the appRecoveries collection with no Edit", gotURL)
	}
	var req struct {
		RemoteInAppUpdate struct {
			IsRemoteInAppUpdateRequested bool `json:"isRemoteInAppUpdateRequested"`
		} `json:"remoteInAppUpdate"`
		Targeting struct {
			Regions struct {
				RegionCode []string `json:"regionCode"`
			} `json:"regions"`
			// androidSdks.sdkLevels is the SDK-level audience dimension: asserted
			// here so a regression in its JSON shape can't ship silently (the
			// builder sets it in internal/play/recovery/recovery.go).
			AndroidSdks struct {
				SdkLevels []int64 `json:"sdkLevels"`
			} `json:"androidSdks"`
			VersionList struct {
				VersionCodes []int64 `json:"versionCodes"`
			} `json:"versionList"`
		} `json:"targeting"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("request body not parseable: %v\n%s", err, gotBody)
	}
	if !req.RemoteInAppUpdate.IsRemoteInAppUpdateRequested {
		t.Error("remoteInAppUpdate should be requested")
	}
	if len(req.Targeting.Regions.RegionCode) != 2 {
		t.Errorf("targeting regions = %v, want [US FR]", req.Targeting.Regions.RegionCode)
	}
	if got := req.Targeting.AndroidSdks.SdkLevels; len(got) != 2 || got[0] != 29 || got[1] != 30 {
		t.Errorf("targeting androidSdks.sdkLevels = %v, want [29 30]", got)
	}
	if len(req.Targeting.VersionList.VersionCodes) != 1 || req.Targeting.VersionList.VersionCodes[0] != 142 {
		t.Errorf("targeting versionCodes = %v, want [142]", req.Targeting.VersionList.VersionCodes)
	}
	if a.AppRecoveryID != "555" || a.Status != "RECOVERY_STATUS_DRAFT" {
		t.Errorf("action = %+v, want draft 555", a)
	}
	if strings.TrimSpace(string(raw)) != draftBody {
		t.Errorf("raw = %s, want verbatim", raw)
	}
}

// TestCreate_allUsersTargeting_onWire asserts the --all-users selector reaches
// the wire as allUsers.isAllUsersRequested: the "everyone" audience dimension,
// distinct from the regions/sdk-levels narrowing. Without this the builder could
// drop or rename the key and no create test would notice.
func TestCreate_allUsersTargeting_onWire(t *testing.T) {
	var gotBody []byte
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotBody, _ = io.ReadAll(r.Body)
		return resp(200, draftBody), nil
	})
	hc := &http.Client{Transport: rt}

	if _, _, err := recovery.Create(context.Background(), hc, "com.example.app", recovery.CreateOpts{
		VersionCodes: []int64{142}, AllUsers: true, RemoteInAppUpdate: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var req struct {
		Targeting struct {
			AllUsers struct {
				IsAllUsersRequested bool `json:"isAllUsersRequested"`
			} `json:"allUsers"`
			Regions *struct{} `json:"regions"`
		} `json:"targeting"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("request body not parseable: %v\n%s", err, gotBody)
	}
	if !req.Targeting.AllUsers.IsAllUsersRequested {
		t.Errorf("targeting.allUsers.isAllUsersRequested = false, want true\n%s", gotBody)
	}
	if req.Targeting.Regions != nil {
		t.Errorf("all-users selector should not emit a regions object: %s", gotBody)
	}
}

// TestList_sendsVersionCode asserts the required versionCode query param and the
// parsed actions.
func TestList_sendsVersionCode(t *testing.T) {
	var gotURL string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return resp(200, `{"recoveryActions":[{"appRecoveryId":"1","status":"RECOVERY_STATUS_ACTIVE"}]}`), nil
	})
	hc := &http.Client{Transport: rt}
	lr, _, err := recovery.List(context.Background(), hc, "com.example.app", 142)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	parsed, perr := url.Parse(gotURL)
	if perr != nil {
		t.Fatalf("parse url %q: %v", gotURL, perr)
	}
	if got := parsed.Query().Get("versionCode"); got != "142" {
		t.Errorf("versionCode param = %q, want exactly 142", got)
	}
	if len(lr.RecoveryActions) != 1 || lr.RecoveryActions[0].AppRecoveryID != "1" {
		t.Errorf("list = %+v, want one action", lr)
	}
}

// TestErrors_mapExitCodes asserts api.Error exit mapping.
func TestErrors_mapExitCodes(t *testing.T) {
	for _, tc := range []struct{ status, want int }{{403, 11}, {404, 30}, {500, 40}} {
		rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return resp(tc.status, `{"error":{"message":"nope"}}`), nil
		})
		hc := &http.Client{Transport: rt}
		_, _, err := recovery.List(context.Background(), hc, "com.example.app", 1)
		var apiErr *api.Error
		if !errors.As(err, &apiErr) || apiErr.ExitCode() != tc.want {
			t.Errorf("status %d → %v, want exit %d", tc.status, err, tc.want)
		}
	}
}
