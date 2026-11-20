package datasafety_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/datasafety"
)

func client(rt http.RoundTripper) *http.Client { return &http.Client{Transport: rt} }

func onlyCall(t *testing.T, fake *testkit.Fake) testkit.Call {
	t.Helper()
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want exactly one POST", len(calls))
	}
	return calls[0]
}

func TestPost_happyPath(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{"safetyLabels":"echo"}`))
	csv := []byte("data_type,collected\nLocation,Yes\n")

	raw, err := datasafety.Post(context.Background(), client(fake), "com.example.app", csv)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	c := onlyCall(t, fake)
	if c.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", c.Method)
	}
	if c.Path != "/androidpublisher/v3/applications/com.example.app/dataSafety" {
		t.Errorf("path = %q, want the non-Edits dataSafety endpoint", c.Path)
	}
	var body struct {
		SafetyLabels string `json:"safetyLabels"`
	}
	if err := json.Unmarshal(c.Body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v\nbody=%s", err, c.Body)
	}
	if body.SafetyLabels != string(csv) {
		t.Errorf("safetyLabels = %q, want the CSV verbatim %q", body.SafetyLabels, csv)
	}
	if string(raw) != `{"safetyLabels":"echo"}` {
		t.Errorf("raw = %s, want the response body verbatim", raw)
	}
}

func TestPost_emptyResponse(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, ""))
	raw, err := datasafety.Post(context.Background(), client(fake), "com.example.app", []byte("a,b\n1,2\n"))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if len(raw) != 0 {
		t.Errorf("raw = %q, want empty on an empty 2xx body", raw)
	}
}

func TestPost_errorStatuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		code     int
		wantExit int
	}{
		{"forbidden", 403, 11},
		{"notFound", 404, 30},
		{"serverError", 500, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := testkit.NewFake(testkit.Any(tc.code, `{"error":{"code":`+itoa(tc.code)+`,"message":"nope"}}`))
			_, err := datasafety.Post(context.Background(), client(fake), "com.example.app", []byte("a\n1\n"))
			var apiErr *api.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v (%T), want *api.Error", err, err)
			}
			if apiErr.StatusCode != tc.code {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.code)
			}
			if got := apiErr.ExitCode(); got != tc.wantExit {
				t.Errorf("ExitCode = %d, want %d", got, tc.wantExit)
			}
		})
	}
}

func TestPost_networkError(t *testing.T) {
	rt := testkit.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})
	_, err := datasafety.Post(context.Background(), client(rt), "com.example.app", []byte("a\n1\n"))
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if apiErr.ExitCode() != 50 {
		t.Errorf("ExitCode = %d, want 50 (network)", apiErr.ExitCode())
	}
}

func itoa(n int) string {
	switch n {
	case 403:
		return "403"
	case 404:
		return "404"
	case 500:
		return "500"
	}
	return "0"
}
