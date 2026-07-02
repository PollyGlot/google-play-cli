package games_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/games"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// recorder captures the last request's method, URL, and body, returning resp.
func recorder(resp *http.Response, gotURL, gotMethod, gotBody *string) *http.Client {
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		*gotURL = r.URL.String()
		*gotMethod = r.Method
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			*gotBody = string(b)
		}
		return resp, nil
	})}
}

func TestListAchievements_addressesApplicationAndPagesThrough(t *testing.T) {
	const body = `{"kind":"x","items":[{"id":"a1"}],"nextPageToken":"tok"}`
	var url, method, reqBody string
	hc := recorder(jsonResp(200, body), &url, &method, &reqBody)
	lr, raw, err := games.ListAchievements(context.Background(), hc, "12345", 50, "page2")
	if err != nil {
		t.Fatalf("ListAchievements: %v", err)
	}
	if method != http.MethodGet {
		t.Errorf("method = %s, want GET", method)
	}
	if !strings.Contains(url, "/applications/12345/achievements") {
		t.Errorf("url %q should address the application's achievements", url)
	}
	if !strings.Contains(url, "maxResults=50") || !strings.Contains(url, "pageToken=page2") {
		t.Errorf("url %q should carry the paging params", url)
	}
	if strings.TrimSpace(string(raw)) != body {
		t.Errorf("raw = %s, want verbatim", raw)
	}
	if len(lr.Items) != 1 || lr.Items[0].ID != "a1" || lr.NextPageToken != "tok" {
		t.Errorf("parsed = %+v, want one item a1 and nextPageToken tok", lr)
	}
}

func TestGetAchievement_addressesResourceID(t *testing.T) {
	var url, method, reqBody string
	hc := recorder(jsonResp(200, `{"id":"a7","draft":{"name":{"translations":[{"locale":"en-US","value":"Win"}]}}}`), &url, &method, &reqBody)
	a, _, err := games.GetAchievement(context.Background(), hc, "a7")
	if err != nil {
		t.Fatalf("GetAchievement: %v", err)
	}
	if !strings.HasSuffix(url, "/achievements/a7") {
		t.Errorf("url %q should address achievement a7", url)
	}
	if got := a.Detail().Name.First(); got != "Win" {
		t.Errorf("name = %q, want Win", got)
	}
}

func TestCreateAchievement_postsBodyToApplication(t *testing.T) {
	var url, method, reqBody string
	hc := recorder(jsonResp(200, `{"id":"new"}`), &url, &method, &reqBody)
	in := []byte(`{"achievementType":"STANDARD","draft":{"name":{"translations":[{"locale":"en-US","value":"Hi"}]}}}`)
	if _, _, err := games.CreateAchievement(context.Background(), hc, "999", in); err != nil {
		t.Fatalf("CreateAchievement: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("method = %s, want POST", method)
	}
	if !strings.Contains(url, "/applications/999/achievements") {
		t.Errorf("url %q should address the application's achievements", url)
	}
	if reqBody != string(in) {
		t.Errorf("body = %s, want %s (verbatim)", reqBody, in)
	}
}

func TestUpdateAchievement_putsBodyToResource(t *testing.T) {
	var url, method, reqBody string
	hc := recorder(jsonResp(200, `{"id":"a7"}`), &url, &method, &reqBody)
	if _, _, err := games.UpdateAchievement(context.Background(), hc, "a7", []byte(`{"stepsToUnlock":5}`)); err != nil {
		t.Fatalf("UpdateAchievement: %v", err)
	}
	if method != http.MethodPut {
		t.Errorf("method = %s, want PUT", method)
	}
	if !strings.HasSuffix(url, "/achievements/a7") {
		t.Errorf("url %q should address achievement a7", url)
	}
}

func TestDeleteAchievement_deletesResource(t *testing.T) {
	var url, method, reqBody string
	hc := recorder(jsonResp(204, ""), &url, &method, &reqBody)
	if err := games.DeleteAchievement(context.Background(), hc, "a7"); err != nil {
		t.Fatalf("DeleteAchievement: %v", err)
	}
	if method != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", method)
	}
	if !strings.HasSuffix(url, "/achievements/a7") {
		t.Errorf("url %q should address achievement a7", url)
	}
}

func TestLeaderboards_addressing(t *testing.T) {
	var url, method, reqBody string
	hc := recorder(jsonResp(200, `{"items":[{"id":"l1","scoreOrder":"LARGER_IS_BETTER"}]}`), &url, &method, &reqBody)
	lr, _, err := games.ListLeaderboards(context.Background(), hc, "55", 0, "")
	if err != nil {
		t.Fatalf("ListLeaderboards: %v", err)
	}
	if !strings.Contains(url, "/applications/55/leaderboards") {
		t.Errorf("url %q should address the application's leaderboards", url)
	}
	if strings.Contains(url, "maxResults") {
		t.Errorf("url %q should omit maxResults when 0", url)
	}
	if len(lr.Items) != 1 || lr.Items[0].ScoreOrder != "LARGER_IS_BETTER" {
		t.Errorf("parsed = %+v", lr)
	}

	hc = recorder(jsonResp(200, `{"id":"l9"}`), &url, &method, &reqBody)
	if _, _, err := games.GetLeaderboard(context.Background(), hc, "l9"); err != nil {
		t.Fatalf("GetLeaderboard: %v", err)
	}
	if !strings.HasSuffix(url, "/leaderboards/l9") {
		t.Errorf("url %q should address leaderboard l9", url)
	}

	hc = recorder(jsonResp(200, `{"id":"l9"}`), &url, &method, &reqBody)
	if err := games.DeleteLeaderboard(context.Background(), hc, "l9"); err != nil {
		t.Fatalf("DeleteLeaderboard: %v", err)
	}
	if method != http.MethodDelete || !strings.HasSuffix(url, "/leaderboards/l9") {
		t.Errorf("delete: method=%s url=%s", method, url)
	}
}

func TestCreateLeaderboard_postsBodyToApplication(t *testing.T) {
	var url, method, reqBody string
	hc := recorder(jsonResp(200, `{"id":"lnew"}`), &url, &method, &reqBody)
	in := []byte(`{"scoreOrder":"LARGER_IS_BETTER","scoreMin":"0"}`)
	if _, _, err := games.CreateLeaderboard(context.Background(), hc, "55", in); err != nil {
		t.Fatalf("CreateLeaderboard: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("method = %s, want POST", method)
	}
	if !strings.Contains(url, "/applications/55/leaderboards") {
		t.Errorf("url %q should address the application's leaderboards", url)
	}
	if reqBody != string(in) {
		t.Errorf("body = %s, want %s (verbatim)", reqBody, in)
	}
}

func TestUpdateLeaderboard_putsBodyToResource(t *testing.T) {
	var url, method, reqBody string
	hc := recorder(jsonResp(200, `{"id":"l9"}`), &url, &method, &reqBody)
	if _, _, err := games.UpdateLeaderboard(context.Background(), hc, "l9", []byte(`{"scoreOrder":"SMALLER_IS_BETTER"}`)); err != nil {
		t.Fatalf("UpdateLeaderboard: %v", err)
	}
	if method != http.MethodPut {
		t.Errorf("method = %s, want PUT", method)
	}
	if !strings.HasSuffix(url, "/leaderboards/l9") {
		t.Errorf("url %q should address leaderboard l9", url)
	}
}

func TestDo_forbiddenMapsToAPIError(t *testing.T) {
	var url, method, reqBody string
	hc := recorder(jsonResp(403, `{"error":{"message":"no access","errors":[{"reason":"forbidden"}]}}`), &url, &method, &reqBody)
	_, _, err := games.GetAchievement(context.Background(), hc, "a7")
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error", err)
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("status = %d, want 403", apiErr.StatusCode)
	}
	if apiErr.ExitCode() != 11 {
		t.Errorf("exit = %d, want 11 (authorization)", apiErr.ExitCode())
	}
}

func TestDo_transportErrorMapsToExit50(t *testing.T) {
	hc := &http.Client{Transport: rtFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})}
	err := games.DeleteAchievement(context.Background(), hc, "a7")
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error", err)
	}
	if apiErr.ExitCode() != 50 {
		t.Errorf("exit = %d, want 50 (transport)", apiErr.ExitCode())
	}
}
