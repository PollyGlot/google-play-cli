package voided_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	voidedcmd "github.com/PollyGlot/google-play-cli/commands/orders/voided"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const page1 = `{
  "tokenPagination": {"nextPageToken": "p2"},
  "voidedPurchases": [
    {"kind": "androidpublisher#voidedPurchase", "orderId": "GPA.0001", "purchaseTimeMillis": "1700000000000", "voidedTimeMillis": "1700086400000", "voidedSource": 2, "voidedReason": 7},
    {"kind": "androidpublisher#voidedPurchase", "orderId": "GPA.0002", "voidedSource": 0, "voidedReason": 1}
  ]
}`

const page2 = `{"voidedPurchases": [{"orderId": "GPA.0003", "voidedSource": 1, "voidedReason": 0, "voidedQuantity": 2}]}`

func newRC(t *testing.T, fake *testkit.Fake) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: fake})
	var stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, &stderr
}

func firstQuery(t *testing.T, fake *testkit.Fake) url.Values {
	t.Helper()
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("no API call recorded")
	}
	q, err := url.ParseQuery(calls[0].Query)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	return q
}

func wantExit(t *testing.T, err error, code int) {
	t.Helper()
	var coder interface{ ExitCode() int }
	if err == nil || !errors.As(err, &coder) || coder.ExitCode() != code {
		t.Fatalf("want exit %d, got %v", code, err)
	}
}

// TestRun_defaults_allTypes_30DaysBack asserts the bare invocation lists in-app
// products and subscriptions (type=1) from 30 days back.
func TestRun_defaults_allTypes_30DaysBack(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	rc, _ := newRC(t, fake)
	before := time.Now()
	if _, err := voidedcmd.Run(rc, voidedcmd.Input{Package: "com.example.app", Since: "30d", Type: "all"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after := time.Now()
	q := firstQuery(t, fake)
	if got := q.Get("type"); got != "1" {
		t.Errorf("type = %q, want 1", got)
	}
	ms, err := strconv.ParseInt(q.Get("startTime"), 10, 64)
	if err != nil {
		t.Fatalf("startTime %q: %v", q.Get("startTime"), err)
	}
	start := time.UnixMilli(ms)
	lo, hi := before.Add(-30*24*time.Hour).Add(-time.Millisecond), after.Add(-30*24*time.Hour)
	if start.Before(lo) || start.After(hi) {
		t.Errorf("startTime = %s, want 30 days back (%s .. %s)", start, lo, hi)
	}
	if q.Has("includeQuantityBasedPartialRefund") {
		t.Errorf("partial refunds requested by default: %v", q)
	}
}

// TestRun_typeInapp_partialRefunds asserts --type inapp sends type=0 and
// --include-partial-refunds the API's partial-refund switch.
func TestRun_typeInapp_partialRefunds(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
	rc, _ := newRC(t, fake)
	if _, err := voidedcmd.Run(rc, voidedcmd.Input{Package: "com.example.app", Since: "7d", Type: "inapp", IncludePartialRefunds: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	q := firstQuery(t, fake)
	if got := q.Get("type"); got != "0" {
		t.Errorf("type = %q, want 0", got)
	}
	if got := q.Get("includeQuantityBasedPartialRefund"); got != "true" {
		t.Errorf("includeQuantityBasedPartialRefund = %q, want true", got)
	}
}

// TestRun_usageErrors_noRequest asserts every malformed input is refused with
// exit 2 before anything reaches the API, the 30-day lookback first among them.
func TestRun_usageErrors_noRequest(t *testing.T) {
	cases := map[string]voidedcmd.Input{
		"since over 30 days": {Package: "com.example.app", Since: "31d", Type: "all"},
		"since in hours":     {Package: "com.example.app", Since: "721h", Type: "all"},
		"malformed since":    {Package: "com.example.app", Since: "soon", Type: "all"},
		"unknown type":       {Package: "com.example.app", Since: "30d", Type: "subs"},
		"negative limit":     {Package: "com.example.app", Since: "30d", Type: "all", Limit: -1},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			fake := testkit.NewFake(testkit.Refuse(t, ""))
			rc, _ := newRC(t, fake)
			_, err := voidedcmd.Run(rc, in)
			wantExit(t, err, 2)
			if n := len(fake.Calls()); n != 0 {
				t.Errorf("calls = %d, want 0", n)
			}
		})
	}
}

// TestRun_limitWarnsOnStderr asserts a capped list keeps the cap and says so
// on stderr.
func TestRun_limitWarnsOnStderr(t *testing.T) {
	fake := testkit.NewFake(testkit.Sequence(page1, page2))
	rc, stderr := newRC(t, fake)
	r, err := voidedcmd.Run(rc, voidedcmd.Input{Package: "com.example.app", Since: "30d", Type: "all", Limit: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatTable, r.Renderers()); err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(buf.String(), "GPA.0003") {
		t.Errorf("capped list leaked the third purchase:\n%s", buf.String())
	}
	if !strings.Contains(stderr.String(), "truncated to 2") {
		t.Errorf("stderr = %q, want a truncation warning", stderr.String())
	}
}

// TestRun_jsonIsTheMergedAPIEnvelope asserts --output json carries every page's
// items under the API's voidedPurchases key.
func TestRun_jsonIsTheMergedAPIEnvelope(t *testing.T) {
	fake := testkit.NewFake(testkit.Sequence(page1, page2))
	rc, _ := newRC(t, fake)
	r, err := voidedcmd.Run(rc, voidedcmd.Input{Package: "com.example.app", Since: "30d", Type: "all"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatJSON, r.Renderers()); err != nil {
		t.Fatalf("render: %v", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, buf.Bytes()); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, buf.String())
	}
	got := compact.String()
	for _, want := range []string{`"voidedPurchases"`, `"kind":"androidpublisher#voidedPurchase"`, `"GPA.0001"`, `"GPA.0003"`, `"voidedQuantity":2`} {
		if !strings.Contains(got, want) {
			t.Errorf("json missing %s:\n%s", want, got)
		}
	}
}

// TestRun_tableNamesSourceAndReason asserts the human view decodes the API's
// numeric source/reason codes and the epoch-millis times.
func TestRun_tableNamesSourceAndReason(t *testing.T) {
	fake := testkit.NewFake(testkit.Sequence(page1, page2))
	rc, _ := newRC(t, fake)
	r, err := voidedcmd.Run(rc, voidedcmd.Input{Package: "com.example.app", Since: "30d", Type: "all"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatTable, r.Renderers()); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := buf.String()
	// 1700086400000 ms = 2023-11-15T22:13:20Z; source 2 = Google, reason 7 = chargeback.
	for _, want := range []string{"ORDER_ID", "VOIDED_SOURCE", "VOIDED_REASON", "2023-11-15T22:13:20Z", "google", "chargeback", "user", "remorse", "developer", "other"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q:\n%s", want, got)
		}
	}
}

// TestRun_403_namesFinancialPermission asserts a refusal names the missing
// permission and keeps the authz exit code.
func TestRun_403_namesFinancialPermission(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusForbidden, `{"error":{"message":"The caller does not have permission"}}`))
	rc, _ := newRC(t, fake)
	_, err := voidedcmd.Run(rc, voidedcmd.Input{Package: "com.example.app", Since: "30d", Type: "all"})
	wantExit(t, err, 11)
	if !strings.Contains(err.Error(), "CAN_VIEW_FINANCIAL_DATA") {
		t.Errorf("error does not name the permission: %v", err)
	}
}
