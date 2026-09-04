// Migration proof for #518: iap now takes its verbs and URLs from
// internal/apiregistry instead of local literals. The pre-existing iap_test.go
// is untouched and still asserts the behaviour; what is added here is the part
// it only checked by substring, the ABSOLUTE URL (host included) and the verb
// of every call, so a change in the Discovery snapshot or in the resolver
// cannot silently move a call to another endpoint.
package iap_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/iap"
)

// pinRT records the absolute URL and verb of the first request and answers an
// empty JSON object, which every call below tolerates.
type pinRT struct {
	url, verb string
}

func (r *pinRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.url == "" {
		r.url, r.verb = req.URL.String(), req.Method
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}, nil
}

const base = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app"

func TestResolvedURLsUnchanged(t *testing.T) {
	cases := []struct {
		name string
		call func(*http.Client) error
		verb string
		want string
	}{
		{
			name: "onetimeproducts.list",
			call: func(hc *http.Client) error {
				_, err := iap.ListOneTimeProducts(context.Background(), hc, "com.example.app")
				return err
			},
			verb: http.MethodGet,
			want: base + "/oneTimeProducts?pageSize=100",
		},
		{
			name: "purchaseOptions.offers.list (wildcard walk)",
			call: func(hc *http.Client) error {
				_, err := iap.ListAllOffers(context.Background(), hc, "com.example.app")
				return err
			},
			verb: http.MethodGet,
			want: base + "/oneTimeProducts/-/purchaseOptions/-/offers?pageSize=100",
		},
		{
			// The snapshot paths the v2 patch under LOWERCASE `onetimeproducts`
			// while every read uses `oneTimeProducts`. That asymmetry is
			// Google's, and this is the line that pins it.
			name: "onetimeproducts.patch",
			call: func(hc *http.Client) error {
				_, err := iap.PatchOneTimeProduct(context.Background(), hc, "com.example.app", "coins", "2022/02", nil, true, json.RawMessage(`{}`))
				return err
			},
			verb: http.MethodPatch,
			want: base + "/onetimeproducts/coins?allowMissing=true&regionsVersion.version=2022%2F02",
		},
		{
			name: "onetimeproducts.delete",
			call: func(hc *http.Client) error {
				return iap.DeleteOneTimeProduct(context.Background(), hc, "com.example.app", "coins")
			},
			verb: http.MethodDelete,
			want: base + "/oneTimeProducts/coins",
		},
		{
			name: "offers.batchUpdate",
			call: func(hc *http.Client) error {
				return iap.BatchUpdateOffers(context.Background(), hc, "com.example.app", "coins", "buy", nil, "2022/02")
			},
			verb: http.MethodPost,
			want: base + "/oneTimeProducts/coins/purchaseOptions/buy/offers:batchUpdate",
		},
		{
			name: "offers.batchDelete",
			call: func(hc *http.Client) error {
				return iap.BatchDeleteOffers(context.Background(), hc, "com.example.app", "coins", "buy", nil)
			},
			verb: http.MethodPost,
			want: base + "/oneTimeProducts/coins/purchaseOptions/buy/offers:batchDelete",
		},
		{
			name: "inappproducts.list (legacy)",
			call: func(hc *http.Client) error {
				_, err := iap.ListInAppProducts(context.Background(), hc, "com.example.app")
				return err
			},
			verb: http.MethodGet,
			want: base + "/inappproducts",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &pinRT{}
			if err := tc.call(&http.Client{Transport: rt}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if rt.url != tc.want {
				t.Errorf("URL = %q, want %q", rt.url, tc.want)
			}
			if rt.verb != tc.verb {
				t.Errorf("verb = %q, want %q", rt.verb, tc.verb)
			}
		})
	}
}

// TestEmptyPackageFailsBeforeTheWire asserts a missing path parameter is
// refused locally, as an *api.Error, rather than sent as a truncated URL.
func TestEmptyPackageFailsBeforeTheWire(t *testing.T) {
	rt := &pinRT{}
	_, err := iap.ListOneTimeProducts(context.Background(), &http.Client{Transport: rt}, "")
	if err == nil {
		t.Fatal("ListOneTimeProducts with an empty package succeeded, want an error")
	}
	if rt.url != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", rt.url)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
}
