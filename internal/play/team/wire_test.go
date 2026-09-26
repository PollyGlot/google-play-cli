package team_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/team"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const dev, email, pkg = "4900000000000000000", "Dev+ops@example.com", "com.example.app"
	list := func(hc *http.Client) (any, error) {
		v, raw, err := team.ListUsers(ctx, hc, dev)
		return []any{v, raw}, err
	}
	sends := []testkit.Send{
		{Name: "ListUsers", Fn: list},
		{Name: "FindUserRaw", Fn: func(hc *http.Client) (any, error) {
			u, raw, found, err := team.FindUserRaw(ctx, hc, dev, email)
			return []any{u, raw, found}, err
		}},
		{Name: "CreateUser", Fn: func(hc *http.Client) (any, error) {
			v, err := team.CreateUser(ctx, hc, dev, email, []string{"CAN_VIEW_FINANCIAL_DATA_GLOBAL"})
			return v, err
		}},
		{Name: "SetUserPermissions none", Fn: func(hc *http.Client) (any, error) {
			v, err := team.SetUserPermissions(ctx, hc, dev, email, nil)
			return v, err
		}},
		{Name: "DeleteUser", Fn: func(hc *http.Client) (any, error) { v, err := team.DeleteUser(ctx, hc, dev, email); return v, err }},
		{Name: "CreateGrant", Fn: func(hc *http.Client) (any, error) {
			v, err := team.CreateGrant(ctx, hc, dev, email, pkg, []string{"CAN_REPLY_TO_REVIEWS"})
			return v, err
		}},
		{Name: "PatchGrant", Fn: func(hc *http.Client) (any, error) {
			v, err := team.PatchGrant(ctx, hc, dev, email, pkg, []string{"CAN_REPLY_TO_REVIEWS", "CAN_VIEW_APP_QUALITY_DATA"})
			return v, err
		}},
		{Name: "DeleteGrant", Fn: func(hc *http.Client) (any, error) {
			v, err := team.DeleteGrant(ctx, hc, dev, email, pkg)
			return v, err
		}},
	}
	var b strings.Builder
	b.WriteString(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed))

	page := func(user, next string) string {
		return `{"users":[{"email":"` + user + `"}],"nextPageToken":"` + next + `"}`
	}
	one := []testkit.Send{{Name: "ListUsers", Fn: list}}
	b.WriteString(testkit.Exchanges(one, testkit.Answer{Name: "two pages", Responders: []testkit.Responder{testkit.Sequence(page("a@example.com", "p/2+"), page("b@example.com", ""))}}))
	b.WriteString(testkit.Exchanges(one, testkit.Answer{Name: "token loop", Responders: []testkit.Responder{testkit.Sequence(page("a@example.com", "again"))}}))
	b.WriteString(testkit.Exchanges(one, testkit.Answer{Name: "bad user", Responders: []testkit.Responder{testkit.Any(http.StatusOK, `{"users":[7]}`)}}))
	testkit.Golden(t, "wire.golden", []byte(b.String()))
}
