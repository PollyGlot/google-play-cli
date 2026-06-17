package recovery_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
)

// TestDeploy_postsDeployVerb asserts the :deploy custom verb path + empty body.
func TestDeploy_postsDeployVerb(t *testing.T) {
	var gotURL, gotMethod string
	var bodyLen int
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		bodyLen = len(b)
		return resp(200, `{}`), nil
	})
	hc := &http.Client{Transport: rt}
	raw, err := recovery.Deploy(context.Background(), hc, "com.example.app", "555")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if gotMethod != http.MethodPost || !strings.HasSuffix(gotURL, "/appRecoveries/555:deploy") {
		t.Errorf("got %s %s, want POST .../appRecoveries/555:deploy", gotMethod, gotURL)
	}
	if bodyLen != 0 {
		t.Errorf("deploy should send an empty body, got %d bytes", bodyLen)
	}
	if strings.TrimSpace(string(raw)) != `{}` {
		t.Errorf("raw = %s, want empty {} passed through verbatim", raw)
	}
}

// TestCancel_postsCancelVerb asserts the :cancel custom verb path.
func TestCancel_postsCancelVerb(t *testing.T) {
	var gotURL string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return resp(200, `{}`), nil
	})
	hc := &http.Client{Transport: rt}
	if _, err := recovery.Cancel(context.Background(), hc, "com.example.app", "555"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if !strings.HasSuffix(gotURL, "/appRecoveries/555:cancel") {
		t.Errorf("url %q should hit the :cancel verb", gotURL)
	}
}

// TestAddTargeting_postsTargetingUpdate asserts the :addTargeting verb + the
// append-only TargetingUpdate body (no versionList).
func TestAddTargeting_postsTargetingUpdate(t *testing.T) {
	var gotURL string
	var gotBody []byte
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotBody, _ = io.ReadAll(r.Body)
		return resp(200, `{}`), nil
	})
	hc := &http.Client{Transport: rt}
	tg := recovery.BuildTargeting(false, []string{"DE"}, nil)
	if _, err := recovery.AddTargeting(context.Background(), hc, "com.example.app", "555", tg); err != nil {
		t.Fatalf("AddTargeting: %v", err)
	}
	if !strings.HasSuffix(gotURL, "/appRecoveries/555:addTargeting") {
		t.Errorf("url %q should hit the :addTargeting verb", gotURL)
	}
	for _, want := range []string{`"targetingUpdate"`, `"regions"`, "DE"} {
		if !strings.Contains(string(gotBody), want) {
			t.Errorf("body %q missing %q", gotBody, want)
		}
	}
	if strings.Contains(string(gotBody), "versionList") {
		t.Errorf("addTargeting body must not carry versionList (append-only user dimension): %s", gotBody)
	}
}
