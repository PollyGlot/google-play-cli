package systemapks_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/systemapks"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const (
	pkg  = "com.example.app"
	base = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/systemApks/42/variants"
)

const variantBody = `{"variantId":3,"deviceSpec":{"screenDensity":480,"supportedAbis":["arm64-v8a","armeabi-v7a"],"supportedLocales":["en-US"]},"options":{"uncompressedNativeLibraries":true}}`

// TestEveryCallAddressesTheVariantsResource pins verb and absolute URL of the
// four calls: application-scoped, never under /edits/.
func TestEveryCallAddressesTheVariantsResource(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		call func(*http.Client) error
		verb string
		want string
	}{
		{"create", func(hc *http.Client) error {
			_, _, err := systemapks.Create(ctx, hc, pkg, 42, systemapks.Variant{})
			return err
		}, http.MethodPost, base},
		{"list", func(hc *http.Client) error {
			_, _, err := systemapks.List(ctx, hc, pkg, 42)
			return err
		}, http.MethodGet, base},
		{"get", func(hc *http.Client) error {
			_, _, err := systemapks.Get(ctx, hc, pkg, 42, 7)
			return err
		}, http.MethodGet, base + "/7"},
		{"download", func(hc *http.Client) error {
			_, err := systemapks.Download(ctx, hc, pkg, 42, 7, io.Discard)
			return err
		}, http.MethodGet, base + "/7:download?alt=media"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
			if err := tc.call(&http.Client{Transport: fake}); err != nil {
				t.Fatalf("call: %v", err)
			}
			calls := fake.Calls()
			if len(calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(calls))
			}
			if calls[0].Method != tc.verb || calls[0].URL != tc.want {
				t.Errorf("sent %s %s, want %s %s", calls[0].Method, calls[0].URL, tc.verb, tc.want)
			}
		})
	}
}

// TestCreate_sendsSpecAndParsesVariant asserts the body carries the device
// spec in the caller's ABI order, omits the server-assigned variantId, and the
// created variant comes back parsed and verbatim.
func TestCreate_sendsSpecAndParsesVariant(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, variantBody))
	req := systemapks.Variant{
		DeviceSpec: &systemapks.DeviceSpec{ScreenDensity: 480, SupportedAbis: []string{"arm64-v8a", "armeabi-v7a"}, SupportedLocales: []string{"en-US"}},
		Options:    &systemapks.Options{UncompressedNativeLibraries: true},
	}
	v, raw, err := systemapks.Create(context.Background(), &http.Client{Transport: fake}, pkg, 42, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sent := string(fake.Calls()[0].Body)
	want := `{"deviceSpec":{"screenDensity":480,"supportedAbis":["arm64-v8a","armeabi-v7a"],"supportedLocales":["en-US"]},"options":{"uncompressedNativeLibraries":true}}`
	if strings.TrimSpace(sent) != want {
		t.Errorf("body = %s, want %s", sent, want)
	}
	if v.VariantID != 3 || v.DeviceSpec == nil || v.DeviceSpec.ScreenDensity != 480 {
		t.Errorf("variant = %+v", v)
	}
	if string(raw) != variantBody {
		t.Errorf("raw = %s, want the verbatim body", raw)
	}
}

func TestList_parsesVariants(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{"variants":[`+variantBody+`]}`))
	lr, _, err := systemapks.List(context.Background(), &http.Client{Transport: fake}, pkg, 42)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(lr.Variants) != 1 || lr.Variants[0].VariantID != 3 {
		t.Errorf("variants = %+v", lr.Variants)
	}
}

func TestDownload_streamsBytes(t *testing.T) {
	const apk = "PK\x03\x04system-apk"
	rt := testkit.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/octet-stream"}}, Body: io.NopCloser(strings.NewReader(apk))}, nil
	})
	var buf bytes.Buffer
	n, err := systemapks.Download(context.Background(), &http.Client{Transport: rt}, pkg, 42, 7, &buf)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if buf.String() != apk || n != int64(len(apk)) {
		t.Errorf("wrote %d bytes %q, want %q", n, buf.String(), apk)
	}
}

func TestErrorsMapToExitCodes(t *testing.T) {
	for status, want := range map[int]int{403: 11, 404: 30} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			fake := testkit.NewFake(testkit.Any(status, `{"error":{"message":"nope"}}`))
			_, _, err := systemapks.Get(context.Background(), &http.Client{Transport: fake}, pkg, 42, 7)
			var apiErr *api.Error
			if !errors.As(err, &apiErr) || apiErr.ExitCode() != want {
				t.Errorf("err = %v, want *api.Error exit %d", err, want)
			}
		})
	}
}
