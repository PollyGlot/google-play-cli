package list_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	listcmd "github.com/PollyGlot/google-play-cli/commands/device-tiers/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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

const listBody = `{"deviceTierConfigs":[{"deviceTierConfigId":"7"},{"deviceTierConfigId":"8"}],"nextPageToken":"next"}`

// TestRun_pagination_and_passthrough asserts page-size/page-token are sent and
// the response (with nextPageToken) is passed through verbatim.
func TestRun_pagination_and_passthrough(t *testing.T) {
	var gotURL string
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(r.URL.Path, "/token") {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`))}, nil
		}
		gotURL = r.URL.String()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(listBody))}, nil
	})
	rc := newRC(t, rt)
	r, err := listcmd.Run(rc, listcmd.Input{Package: "com.example.app", PageSize: 25, PageToken: "tok"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"pageSize=25", "pageToken=tok"} {
		if !strings.Contains(gotURL, want) {
			t.Errorf("url %q missing %q", gotURL, want)
		}
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"nextPageToken":"next"`) {
		t.Errorf("json %s should preserve nextPageToken", out.String())
	}
}
