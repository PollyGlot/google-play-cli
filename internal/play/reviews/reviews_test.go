package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// callLine renders a recorded call as "METHOD path?query", so a test can
// assert the wire calls of a paginated list.
func callLine(c testkit.Call) string { return c.Method + " " + c.Path + "?" + c.Query }

func callLines(fake *testkit.Fake) []string {
	var lines []string
	for _, c := range fake.Calls() {
		lines = append(lines, callLine(c))
	}
	return lines
}

// onlyCall returns the single request the Fake recorded, failing otherwise.
func onlyCall(t *testing.T, fake *testkit.Fake) testkit.Call {
	t.Helper()
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want exactly one", len(calls))
	}
	return calls[0]
}

// loopingFake always advertises the same nextPageToken, simulating an API that
// cycles a pagination token. It self-limits so an unguarded loop fails the
// test fast instead of hanging.
func loopingFake(t *testing.T) *testkit.Fake {
	t.Helper()
	var (
		mu sync.Mutex
		n  int
	)
	return testkit.NewFake(func(testkit.Call) (int, string, bool) {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n > 10 {
			t.Fatalf("List made %d calls without terminating: pagination token loop not guarded", n)
		}
		return http.StatusOK, `{"reviews":[{"reviewId":"r","comments":[{"userComment":{"starRating":5,"reviewerLanguage":"en"}}]}],"tokenPagination":{"nextPageToken":"LOOP"}}`, true
	})
}

func TestReply_postsReplyTextAndReturnsRawBody(t *testing.T) {
	rt := testkit.NewFake(testkit.Any(http.StatusOK, `{"result":{"replyText":"thanks","lastEdited":{"seconds":"1700000000"}}}`))
	hc := &http.Client{Transport: rt}

	raw, err := Reply(context.Background(), hc, "com.example.app", "gp:AOqpT123", "thanks")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}

	c := onlyCall(t, rt)
	if c.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", c.Method)
	}
	// The reviewId's own colon and the :reply custom-method colon both stay
	// literal in the path.
	wantPath := "/androidpublisher/v3/applications/com.example.app/reviews/gp:AOqpT123:reply"
	if c.Path != wantPath {
		t.Errorf("path = %q, want %q", c.Path, wantPath)
	}
	// The body carries the reply under the API's replyText field.
	var sent struct {
		ReplyText string `json:"replyText"`
	}
	if err := json.Unmarshal(c.Body, &sent); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, c.Body)
	}
	if sent.ReplyText != "thanks" {
		t.Errorf("replyText = %q, want %q", sent.ReplyText, "thanks")
	}
	// The response is passed through verbatim for --output json (ADR-0003).
	if !json.Valid(raw) {
		t.Errorf("raw is not valid JSON: %s", raw)
	}
	if !strings.Contains(string(raw), "thanks") {
		t.Errorf("raw should echo the API response, got: %s", raw)
	}
}

func TestReply_nonOKBecomesAPIErrorWithStatus(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		wantExit int // via the shared StatusToExitCode taxonomy
	}{
		{"forbidden", 403, 11},
		{"notFound", 404, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := testkit.NewFake(testkit.Any(tc.code, `{"error":{"code":`+strconv.Itoa(tc.code)+`,"message":"nope"}}`))
			hc := &http.Client{Transport: rt}

			_, err := Reply(context.Background(), hc, "com.example.app", "r1", "hi")
			var apiErr *api.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v (%T), want *api.Error", err, err)
			}
			if apiErr.StatusCode != tc.code {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.code)
			}
			if apiErr.ExitCode() != tc.wantExit {
				t.Errorf("ExitCode() = %d, want %d", apiErr.ExitCode(), tc.wantExit)
			}
		})
	}
}

func TestList_stopsOnRepeatedPaginationToken(t *testing.T) {
	rt := loopingFake(t)
	hc := &http.Client{Transport: rt}

	_, err := List(context.Background(), hc, "com.example.app")
	if err == nil {
		t.Fatal("expected an error when the API repeats a pagination token, got nil")
	}
	// First call yields LOOP (new), second call repeats LOOP → detected.
	if n := len(rt.Calls()); n != 2 {
		t.Errorf("expected exactly 2 calls before detecting the loop, got %d", n)
	}
}

func TestList_autoPaginates(t *testing.T) {
	page1 := `{"reviews":[{"reviewId":"r1","comments":[{"userComment":{"text":"a","starRating":5,"reviewerLanguage":"en"}}]}],"tokenPagination":{"nextPageToken":"PAGE2"}}`
	page2 := `{"reviews":[{"reviewId":"r2","comments":[{"userComment":{"text":"b","starRating":3,"reviewerLanguage":"en"}}]}],"tokenPagination":{"nextPageToken":"PAGE3"}}`
	page3 := `{"reviews":[{"reviewId":"r3","comments":[{"userComment":{"text":"c","starRating":1,"reviewerLanguage":"en"}}]}]}` // no nextPageToken → stop
	rt := testkit.NewFake(testkit.Sequence(page1, page2, page3))
	hc := &http.Client{Transport: rt}

	got, err := List(context.Background(), hc, "com.example.app")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d reviews across pages, want 3", len(got))
	}
	wantIDs := []string{"r1", "r2", "r3"}
	for i, id := range wantIDs {
		if got[i].ReviewID != id {
			t.Errorf("got[%d].ReviewID = %q, want %q", i, got[i].ReviewID, id)
		}
	}

	// Three calls: the first with no token, then the nextPageToken of each
	// prior page carried forward in the `token` query param. Pagination
	// stops when a page omits nextPageToken.
	calls := callLines(rt)
	if len(calls) != 3 {
		t.Fatalf("made %d calls, want 3: %v", len(calls), calls)
	}
	if got := calls[1]; !strings.Contains(got, "token=PAGE2") {
		t.Errorf("2nd call = %q, want token=PAGE2", got)
	}
	if got := calls[2]; !strings.Contains(got, "token=PAGE3") {
		t.Errorf("3rd call = %q, want token=PAGE3", got)
	}
}

func TestList_singlePage(t *testing.T) {
	body := `{"reviews":[
		{"reviewId":"r1","comments":[{"userComment":{"text":"Great app\nsecond line","starRating":5,"reviewerLanguage":"en","lastModified":{"seconds":"1700000000","nanos":0}}}]},
		{"reviewId":"r2","comments":[{"userComment":{"text":"Bad","starRating":1,"reviewerLanguage":"fr-FR","lastModified":{"seconds":"1700000100"}}}]}
	]}`
	rt := testkit.NewFake(testkit.Sequence(body))
	hc := &http.Client{Transport: rt}

	got, err := List(context.Background(), hc, "com.example.app")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d reviews, want 2", len(got))
	}

	// The call lands on the reviews collection of the package, with no edit.
	wantCall := "GET /androidpublisher/v3/applications/com.example.app/reviews?"
	if calls := callLines(rt); len(calls) != 1 || calls[0] != wantCall {
		t.Errorf("calls = %v, want exactly [%q]", calls, wantCall)
	}

	// Parsed view, read through the public accessors.
	if got[0].ReviewID != "r1" {
		t.Errorf("got[0].ReviewID = %q, want r1", got[0].ReviewID)
	}
	if got[0].Stars() != 5 {
		t.Errorf("got[0].Stars() = %d, want 5", got[0].Stars())
	}
	if got[0].Locale() != "en" {
		t.Errorf("got[0].Locale() = %q, want en", got[0].Locale())
	}
	if got[1].Stars() != 1 || got[1].Locale() != "fr-FR" {
		t.Errorf("got[1] = stars %d locale %q, want 1/fr-FR", got[1].Stars(), got[1].Locale())
	}

	// LastModified decodes the unix seconds into a UTC instant.
	if ts := got[0].LastModified(); ts.IsZero() || ts.Unix() != 1700000000 {
		t.Errorf("got[0].LastModified() = %v, want unix 1700000000", got[0].LastModified())
	}

	// Each review keeps its verbatim JSON object for the pass-through.
	if !json.Valid(got[0].Raw) {
		t.Errorf("got[0].Raw is not valid JSON: %s", got[0].Raw)
	}
	var rawID struct {
		ReviewID string `json:"reviewId"`
	}
	if err := json.Unmarshal(got[0].Raw, &rawID); err != nil || rawID.ReviewID != "r1" {
		t.Errorf("got[0].Raw did not round-trip reviewId=r1: %v / %s", err, got[0].Raw)
	}
}

func TestGet_fetchesSingleReviewAndParsesThread(t *testing.T) {
	body := `{
		"reviewId":"gp:AOqpT123",
		"authorName":"Jane Doe",
		"comments":[
			{"userComment":{"text":"Crashes on launch","starRating":2,"reviewerLanguage":"en-US","device":"flame","appVersionName":"1.2.3","appVersionCode":45,"lastModified":{"seconds":"1700000000"}}},
			{"developerComment":{"text":"Sorry — fixed in 1.2.4","lastModified":{"seconds":"1700000600"}}}
		]
	}`
	rt := testkit.NewFake(testkit.Any(http.StatusOK, body))
	hc := &http.Client{Transport: rt}

	got, err := Get(context.Background(), hc, "com.example.app", "gp:AOqpT123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	c := onlyCall(t, rt)
	if c.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", c.Method)
	}
	// The reviewId's own colon stays literal in the path (no :reply suffix).
	wantPath := "/androidpublisher/v3/applications/com.example.app/reviews/gp:AOqpT123"
	if c.Path != wantPath {
		t.Errorf("path = %q, want %q", c.Path, wantPath)
	}

	// Header accessors.
	if got.ReviewID != "gp:AOqpT123" {
		t.Errorf("ReviewID = %q, want gp:AOqpT123", got.ReviewID)
	}
	if got.Author() != "Jane Doe" {
		t.Errorf("Author() = %q, want Jane Doe", got.Author())
	}
	if got.Stars() != 2 {
		t.Errorf("Stars() = %d, want 2", got.Stars())
	}
	if got.Locale() != "en-US" {
		t.Errorf("Locale() = %q, want en-US", got.Locale())
	}
	if got.Device() != "flame" {
		t.Errorf("Device() = %q, want flame", got.Device())
	}
	if got.AppVersion() != "1.2.3 (45)" {
		t.Errorf("AppVersion() = %q, want \"1.2.3 (45)\"", got.AppVersion())
	}
	if ts := got.LastModified(); ts.Unix() != 1700000000 {
		t.Errorf("LastModified() = %v, want unix 1700000000", ts)
	}

	// The developer reply thread is parsed in conversation order.
	replies := got.DeveloperReplies()
	if len(replies) != 1 {
		t.Fatalf("DeveloperReplies() = %d, want 1", len(replies))
	}
	if replies[0].Text != "Sorry — fixed in 1.2.4" {
		t.Errorf("reply text = %q", replies[0].Text)
	}
	if ts := replies[0].LastModified.Time(); ts.Unix() != 1700000600 {
		t.Errorf("reply LastModified = %v, want unix 1700000600", ts)
	}

	// The verbatim body is retained for the --output json pass-through.
	if !json.Valid(got.Raw) || !strings.Contains(string(got.Raw), "gp:AOqpT123") {
		t.Errorf("Raw should echo the API body verbatim, got: %s", got.Raw)
	}
}

func TestGet_appVersionPartialAndAbsent(t *testing.T) {
	cases := []struct {
		name string
		uc   string
		want string
	}{
		{"both", `"appVersionName":"2.0","appVersionCode":99`, "2.0 (99)"},
		{"nameOnly", `"appVersionName":"2.0"`, "2.0"},
		{"codeOnly", `"appVersionCode":99`, "(99)"},
		{"neither", `"text":"hi"`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"reviewId":"r1","comments":[{"userComment":{` + tc.uc + `}}]}`
			rt := testkit.NewFake(testkit.Any(http.StatusOK, body))
			got, err := Get(context.Background(), &http.Client{Transport: rt}, "com.example.app", "r1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.AppVersion() != tc.want {
				t.Errorf("AppVersion() = %q, want %q", got.AppVersion(), tc.want)
			}
		})
	}
}

func TestGet_nonOKBecomesAPIErrorWithStatus(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		wantExit int // via the shared StatusToExitCode taxonomy
	}{
		{"forbidden", 403, 11},
		{"notFound", 404, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := testkit.NewFake(testkit.Any(tc.code, `{"error":{"code":`+strconv.Itoa(tc.code)+`,"message":"nope"}}`))
			hc := &http.Client{Transport: rt}

			_, err := Get(context.Background(), hc, "com.example.app", "r1")
			var apiErr *api.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v (%T), want *api.Error", err, err)
			}
			if apiErr.StatusCode != tc.code {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.code)
			}
			if apiErr.ExitCode() != tc.wantExit {
				t.Errorf("ExitCode() = %d, want %d", apiErr.ExitCode(), tc.wantExit)
			}
		})
	}
}
