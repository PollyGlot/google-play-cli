package convert_test

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
	"sync"
	"testing"

	"golang.org/x/oauth2"

	convertcmd "github.com/PollyGlot/google-play-cli/commands/subscriptions/prices/convert"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const convertBody = `{
  "regionVersion": {"version": "2022/02"},
  "convertedRegionPrices": {
    "FR": {"regionCode": "FR", "price": {"currencyCode": "EUR", "units": "4", "nanos": 490000000}},
    "BR": {"regionCode": "BR", "price": {"currencyCode": "BRL", "units": "24", "nanos": 990000000}}
  }
}`

type convertRT struct {
	mu    sync.Mutex
	calls []string
	body  string
}

func (r *convertRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		r.body = string(b)
	}
	return jsonResp(200, convertBody), nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// TestParseMoney_shapes asserts the decimal-to-Money conversion.
func TestParseMoney_shapes(t *testing.T) {
	m, err := convertcmd.ParseMoney("4.99", "usd")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	if m.CurrencyCode != "USD" || m.Units != "4" || m.Nanos != 990000000 {
		t.Errorf("m = %+v, want USD 4 + 990000000", m)
	}
	m, err = convertcmd.ParseMoney("10", "EUR")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	if m.Units != "10" || m.Nanos != 0 {
		t.Errorf("m = %+v, want 10 + 0", m)
	}
	for _, bad := range [][2]string{{"", "USD"}, {"4,99", "USD"}, {"-1", "USD"}, {"4.9999999999", "USD"}, {"4.99", "US"}} {
		if _, err := convertcmd.ParseMoney(bad[0], bad[1]); err == nil {
			t.Errorf("ParseMoney(%q,%q) should refuse", bad[0], bad[1])
		}
	}
}

// TestRun_convertsAndPassesThrough asserts the POST hits the pricing endpoint,
// the table is one sorted line per region, and json is verbatim.
func TestRun_convertsAndPassesThrough(t *testing.T) {
	rt := &convertRT{}
	rc := newRC(t, rt)
	r, err := convertcmd.Run(rc, convertcmd.Input{Package: "com.example.app", Price: "4.99", Currency: "USD"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.calls) != 1 || !strings.Contains(rt.calls[0], "pricing:convertRegionPrices") {
		t.Fatalf("calls = %v, want the pricing:convertRegionPrices POST", rt.calls)
	}
	if !strings.Contains(rt.body, `"currencyCode":"USD"`) {
		t.Errorf("request body %q must carry the base Money", rt.body)
	}
	var tbl bytes.Buffer
	if err := r.Renderers().Table(&tbl); err != nil {
		t.Fatalf("Table: %v", err)
	}
	got := tbl.String()
	if !strings.Contains(got, "FR\t4.49 EUR") || !strings.Contains(got, "BR\t24.99 BRL") {
		t.Errorf("table %q missing converted rows", got)
	}
	if strings.Index(got, "BR") > strings.Index(got, "FR") {
		t.Errorf("table %q should sort regions", got)
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(js.String(), "regionVersion") {
		t.Errorf("json %s should pass the response through verbatim", js.String())
	}
}

// TestRun_badPrice_exit2_noNetwork asserts an invalid amount is CLI misuse
// before any HTTP call.
func TestRun_badPrice_exit2_noNetwork(t *testing.T) {
	rt := &convertRT{}
	rc := newRC(t, rt)
	_, err := convertcmd.Run(rc, convertcmd.Input{Package: "com.example.app", Price: "abc", Currency: "USD"})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v has no ExitCode", err)
	}
	if got := c.ExitCode(); got != want {
		t.Errorf("exit = %d, want %d", got, want)
	}
}
