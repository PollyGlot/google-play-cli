// Migration proof for #519. team was the one package of batch 3 with no test
// at all, so its seven URLs were unpinned before the migration and would have
// been unpinned after it. This file closes that: it drives every exported call
// against a recorder and asserts the absolute URL (host included), the verb
// and the query suffix that carries the declarative updateMask, which is what
// makes the requests provably byte-identical to the hand-built ones.
package team_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/team"
)

const devRoot = "https://androidpublisher.googleapis.com/androidpublisher/v3/developers/1234567890"

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// recorder captures the last request's absolute URL and verb, answering with
// an empty JSON object (enough for every call here: ListUsers reads `users`
// and `nextPageToken`, the writes pass the body through verbatim).
func recorder(gotURL, gotVerb *string) *http.Client {
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		*gotURL = r.URL.String()
		*gotVerb = r.Method
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})}
}

func TestAbsoluteURLsUnchanged(t *testing.T) {
	const (
		dev   = "1234567890"
		email = "dev@example.com"
		pkg   = "com.example.app"
	)
	cases := []struct {
		name     string
		call     func(*http.Client) error
		wantURL  string
		wantVerb string
	}{
		{
			name: "users.list",
			call: func(hc *http.Client) error {
				_, _, err := team.ListUsers(context.Background(), hc, dev)
				return err
			},
			wantURL:  devRoot + "/users?pageSize=100",
			wantVerb: http.MethodGet,
		},
		{
			name: "users.create",
			call: func(hc *http.Client) error {
				_, err := team.CreateUser(context.Background(), hc, dev, email, []string{"CAN_VIEW_FINANCIAL_DATA_GLOBAL"})
				return err
			},
			wantURL:  devRoot + "/users",
			wantVerb: http.MethodPost,
		},
		{
			name: "users.patch",
			call: func(hc *http.Client) error {
				_, err := team.SetUserPermissions(context.Background(), hc, dev, email, nil)
				return err
			},
			// url.PathEscape leaves @ alone (it is a legal path sub-delim), so the
			// email appears literally, exactly as the hand-built URL had it.
			wantURL:  devRoot + "/users/dev@example.com?updateMask=developerAccountPermissions",
			wantVerb: http.MethodPatch,
		},
		{
			name: "users.delete",
			call: func(hc *http.Client) error {
				_, err := team.DeleteUser(context.Background(), hc, dev, email)
				return err
			},
			wantURL:  devRoot + "/users/dev@example.com",
			wantVerb: http.MethodDelete,
		},
		{
			name: "grants.create",
			call: func(hc *http.Client) error {
				_, err := team.CreateGrant(context.Background(), hc, dev, email, pkg, nil)
				return err
			},
			wantURL:  devRoot + "/users/dev@example.com/grants",
			wantVerb: http.MethodPost,
		},
		{
			name: "grants.patch",
			call: func(hc *http.Client) error {
				_, err := team.PatchGrant(context.Background(), hc, dev, email, pkg, nil)
				return err
			},
			wantURL:  devRoot + "/users/dev@example.com/grants/com.example.app?updateMask=appLevelPermissions",
			wantVerb: http.MethodPatch,
		},
		{
			name: "grants.delete",
			call: func(hc *http.Client) error {
				_, err := team.DeleteGrant(context.Background(), hc, dev, email, pkg)
				return err
			},
			wantURL:  devRoot + "/users/dev@example.com/grants/com.example.app",
			wantVerb: http.MethodDelete,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotURL, gotVerb string
			hc := recorder(&gotURL, &gotVerb)
			if err := tc.call(hc); err != nil {
				t.Fatalf("call: %v", err)
			}
			if gotURL != tc.wantURL {
				t.Errorf("URL = %q, want %q", gotURL, tc.wantURL)
			}
			if gotVerb != tc.wantVerb {
				t.Errorf("verb = %q, want %q", gotVerb, tc.wantVerb)
			}
		})
	}
}

// TestEmptyPathParameterFailsBeforeTheWire asserts a missing path parameter is
// refused locally rather than sent as a truncated URL that would 404 far from
// its cause.
func TestEmptyPathParameterFailsBeforeTheWire(t *testing.T) {
	var gotURL, gotVerb string
	hc := recorder(&gotURL, &gotVerb)
	if _, err := team.DeleteUser(context.Background(), hc, "", "dev@example.com"); err == nil {
		t.Fatal("DeleteUser with an empty developer id succeeded, want an error")
	}
	if gotURL != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", gotURL)
	}
}
