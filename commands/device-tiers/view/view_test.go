package view_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	viewcmd "github.com/PollyGlot/google-play-cli/commands/device-tiers/view"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func token(r *http.Request) (*http.Response, bool) {
	if r.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(r.URL.Path, "/token") {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`))}, true
	}
	return nil, false
}

func jsonResp(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func saJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

const configBody = `{"deviceTierConfigId":"42","deviceGroups":[{"name":"high"}]}`

// TestRun_happyPath_addressesIDAndPassesThrough asserts the GET addresses the id
// and the JSON view is verbatim.
func TestRun_happyPath_addressesIDAndPassesThrough(t *testing.T) {
	var gotURL string
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := token(r); ok {
			return resp, nil
		}
		gotURL = r.URL.String()
		return jsonResp(configBody), nil
	})
	rc := newRC(t, rt)
	r, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app", ID: "42"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasSuffix(gotURL, "/deviceTierConfigs/42") {
		t.Errorf("url %q should address config 42", gotURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.TrimSpace(out.String()) != configBody {
		t.Errorf("json = %s, want verbatim", out.String())
	}
}

// TestRun_missingID_exit2 asserts an empty id is CLI misuse.
func TestRun_missingID_exit2(t *testing.T) {
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := token(r); ok {
			return resp, nil
		}
		t.Fatalf("unexpected call: %s", r.URL)
		return nil, nil
	})
	rc := newRC(t, rt)
	_, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
}
