package testkit_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestRSAKey_isSharedAcrossCalls(t *testing.T) {
	first, second := testkit.RSAKey(t), testkit.RSAKey(t)
	if first != second {
		t.Fatal("RSAKey must return the one key generated for the test binary")
	}
	if got := testkit.RSAKey(t).N.BitLen(); got != 2048 {
		t.Errorf("key size = %d bits, want 2048", got)
	}
}

func TestPrivateKeyPEM_isPKCS8OfTheSharedKey(t *testing.T) {
	block, _ := pem.Decode(testkit.PrivateKeyPEM(t))
	if block == nil || block.Type != "PRIVATE KEY" {
		t.Fatalf("PEM block = %+v, want a PRIVATE KEY block", block)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKCS8PrivateKey: %v", err)
	}
	if !testkit.RSAKey(t).Equal(key) {
		t.Error("PEM must encode the shared key")
	}
}

func TestServiceAccountJSON_fields(t *testing.T) {
	var sa map[string]string
	if err := json.Unmarshal(testkit.ServiceAccountJSON(t), &sa); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]string{
		"type":         "service_account",
		"project_id":   testkit.ProjectID,
		"client_email": testkit.ClientEmail,
		"token_uri":    testkit.TokenURL,
	}
	for k, v := range want {
		if sa[k] != v {
			t.Errorf("%s = %q, want %q", k, sa[k], v)
		}
	}
	if !strings.Contains(sa["private_key"], "BEGIN PRIVATE KEY") {
		t.Error("private_key must carry the PKCS8 PEM")
	}
}

// The fixture is only useful if the real oauth2 JWT flow signs with it and
// lands on the fake: this drives golang.org/x/oauth2/google end to end.
func TestFake_servesTheJWTExchangeOfTheFixture(t *testing.T) {
	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		return 200, `{"ok":true}`, c.Method == http.MethodGet && c.Path == "/v1/things"
	})
	cfg, err := google.JWTConfigFromJSON(testkit.ServiceAccountJSON(t), "https://www.googleapis.com/auth/androidpublisher")
	if err != nil {
		t.Fatalf("JWTConfigFromJSON: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: fake})
	resp, err := cfg.Client(ctx).Get("https://androidpublisher.googleapis.com/v1/things")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != `{"ok":true}` {
		t.Errorf("response = %d %s", resp.StatusCode, body)
	}
	if fake.TokenExchanges() != 1 {
		t.Errorf("token exchanges = %d, want 1", fake.TokenExchanges())
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Header.Get("Authorization") != "Bearer a.b.c" {
		t.Errorf("calls = %+v, want one call carrying the fake bearer token", calls)
	}
}

func TestFake_rejectsTokenRequestsThatOnlyEndInToken(t *testing.T) {
	for _, u := range []string{
		"https://androidpublisher.googleapis.com/androidpublisher/v3/token",
		"https://oauth2.example.com/token",
		"https://evil.example/oauth2.googleapis.com/token",
		"http://oauth2.googleapis.com/token",
		"https://oauth2.googleapis.com/v4/token",
		"https://oauth2.googleapis.com/token?x=1",
	} {
		t.Run(u, func(t *testing.T) {
			fake := testkit.NewFake()
			req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader("grant_type=x"))
			resp, err := fake.RoundTrip(req)
			if err == nil {
				t.Fatalf("got %d, want the round trip rejected (no bearer token for %s)", resp.StatusCode, u)
			}
			if fake.TokenExchanges() != 0 {
				t.Errorf("token exchanges = %d, want 0", fake.TokenExchanges())
			}
			if len(fake.Calls()) != 1 {
				t.Errorf("the rejected request must be recorded as an API call, calls = %+v", fake.Calls())
			}
		})
	}
}

func TestFake_respondersInOrderAndWrote(t *testing.T) {
	fake := testkit.NewFake(
		func(c testkit.Call) (int, string, bool) { return 0, `first`, c.Method == http.MethodGet },
		testkit.Any(404, `fallback`),
	)
	get, _ := http.NewRequest(http.MethodGet, "https://example.test/a", nil)
	resp, err := fake.RoundTrip(get)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET = %v %v, want 200 (status 0 means 200)", resp, err)
	}
	if fake.Wrote() {
		t.Error("a GET is not a write")
	}
	patch, _ := http.NewRequest(http.MethodPatch, "https://example.test/a?b=c", strings.NewReader(`{"x":1}`))
	resp, err = fake.RoundTrip(patch)
	if err != nil || resp.StatusCode != 404 {
		t.Fatalf("PATCH = %v %v, want the 404 fallback", resp, err)
	}
	if !fake.Wrote() {
		t.Error("a PATCH is a write")
	}
	c := fake.Calls()[1]
	if c.Query != "b=c" || string(c.Body) != `{"x":1}` || c.Host != "example.test" {
		t.Errorf("recorded call = %+v", c)
	}
}

func TestGolden_matchesCommittedFile(t *testing.T) {
	testkit.Golden(t, "example.golden", []byte("line one\nline two\n"))
}

// recorder captures what Golden reports instead of failing this test.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func TestGolden_reportsTheFirstDifferingLine(t *testing.T) {
	r := &recorder{TB: t}
	testkit.Golden(r, "example.golden", []byte("line one\nline 2\n"))
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], `line 2:`) || !strings.Contains(r.errs[0], `"line two"`) {
		t.Errorf("errors = %q, want one report pointing at line 2", r.errs)
	}
}
