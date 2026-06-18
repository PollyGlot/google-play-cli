package devicetiers_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/devicetiers"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const createdBody = `{"deviceTierConfigId":"42","deviceGroups":[{"name":"high"},{"name":"low"}],"deviceTierSet":{"deviceTiers":[{"level":1},{"level":0}]},"userCountrySets":[{"name":"latam"}]}`

// TestCreate_postsBody_noEdit asserts Create posts the body to the
// deviceTierConfigs collection (no /edits/ segment) and returns the created
// config + verbatim raw.
func TestCreate_postsBody_noEdit(t *testing.T) {
	var gotURL, gotMethod string
	var gotBody []byte
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		return resp(200, createdBody), nil
	})
	hc := &http.Client{Transport: rt}

	cfg, raw, err := devicetiers.Create(context.Background(), hc, "com.example.app", []byte(`{"deviceGroups":[{"name":"high"}]}`), false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotURL, "/applications/com.example.app/deviceTierConfigs") {
		t.Errorf("url %q should be the collection endpoint with no /edits/ segment", gotURL)
	}
	if strings.Contains(gotURL, "/edits/") {
		t.Errorf("device tier configs are NOT an Edit resource; url %q must not contain /edits/", gotURL)
	}
	if !strings.Contains(string(gotBody), `"deviceGroups"`) {
		t.Errorf("request body %q should be the posted config", gotBody)
	}
	if cfg.DeviceTierConfigID != "42" || len(cfg.DeviceGroups) != 2 || cfg.Tiers() != 2 || len(cfg.UserCountrySets) != 1 {
		t.Errorf("parsed config = %+v, want id 42 / 2 groups / 2 tiers / 1 countrySet", cfg)
	}
	if strings.TrimSpace(string(raw)) != createdBody {
		t.Errorf("raw = %s, want verbatim pass-through", raw)
	}
}

// TestCreate_allowUnknownDevices_queryParam asserts the flag adds the query
// param only when true.
func TestCreate_allowUnknownDevices_queryParam(t *testing.T) {
	var gotURL string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return resp(200, createdBody), nil
	})
	hc := &http.Client{Transport: rt}
	if _, _, err := devicetiers.Create(context.Background(), hc, "com.example.app", []byte(`{}`), true); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(gotURL, "allowUnknownDevices=true") {
		t.Errorf("url %q should carry allowUnknownDevices=true", gotURL)
	}
}

// TestList_paginationAndPassthrough asserts pageSize/pageToken are sent and the
// nextPageToken is preserved.
func TestList_paginationAndPassthrough(t *testing.T) {
	var gotURL string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return resp(200, `{"deviceTierConfigs":[{"deviceTierConfigId":"7"}],"nextPageToken":"next"}`), nil
	})
	hc := &http.Client{Transport: rt}
	lr, raw, err := devicetiers.List(context.Background(), hc, "com.example.app", 50, "tok")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, want := range []string{"pageSize=50", "pageToken=tok"} {
		if !strings.Contains(gotURL, want) {
			t.Errorf("url %q missing %q", gotURL, want)
		}
	}
	if lr.NextPageToken != "next" || len(lr.DeviceTierConfigs) != 1 {
		t.Errorf("list = %+v, want 1 config + nextPageToken", lr)
	}
	if !strings.Contains(string(raw), `"nextPageToken":"next"`) {
		t.Errorf("raw %s should preserve nextPageToken", raw)
	}
}

// TestGet_byID asserts the single-resource path.
func TestGet_byID(t *testing.T) {
	var gotURL string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return resp(200, `{"deviceTierConfigId":"42"}`), nil
	})
	hc := &http.Client{Transport: rt}
	if _, _, err := devicetiers.Get(context.Background(), hc, "com.example.app", "42"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.HasSuffix(gotURL, "/deviceTierConfigs/42") {
		t.Errorf("url %q should address config 42", gotURL)
	}
}

// TestErrors_mapExitCodes asserts 403→11, 404→30, 500→40 via *api.Error.
func TestErrors_mapExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status, want int
	}{{403, 11}, {404, 30}, {500, 40}} {
		rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return resp(tc.status, `{"error":{"message":"nope"}}`), nil
		})
		hc := &http.Client{Transport: rt}
		_, _, err := devicetiers.Get(context.Background(), hc, "com.example.app", "1")
		var apiErr *api.Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("status %d: err = %v (%T), want *api.Error", tc.status, err, err)
		}
		if apiErr.ExitCode() != tc.want {
			t.Errorf("status %d → exit %d, want %d", tc.status, apiErr.ExitCode(), tc.want)
		}
	}
}
