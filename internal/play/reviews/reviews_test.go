package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// reviewsRT is a RoundTripper that serves a fixed sequence of canned
// reviews.list pages and records the request path+query it saw for each
// call, so a test can assert both the parsed result and the wire calls.
type reviewsRT struct {
	pages []string // JSON bodies, served in order

	mu    sync.Mutex
	calls []string // "METHOD path?query" per request
	n     int
}

func (r *reviewsRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req.Method+" "+req.URL.Path+"?"+req.URL.RawQuery)
	body := "{}"
	if r.n < len(r.pages) {
		body = r.pages[r.n]
	}
	r.n++
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

// loopingRT always advertises the same nextPageToken, simulating an API that
// cycles a pagination token. It self-limits so an unguarded loop fails the
// test fast instead of hanging.
type loopingRT struct {
	t     *testing.T
	calls int
}

func (r *loopingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	if r.calls > 10 {
		r.t.Fatalf("List made %d calls without terminating — pagination token loop not guarded", r.calls)
	}
	body := `{"reviews":[{"reviewId":"r","comments":[{"userComment":{"starRating":5,"reviewerLanguage":"en"}}]}],"tokenPagination":{"nextPageToken":"LOOP"}}`
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

// replyRT captures the reviews.reply POST — method, path, and request body —
// and serves a canned response. code/errBody force a non-2xx for the
// error-mapping test (0 → 200).
type replyRT struct {
	code     int
	respBody string
	errBody  string

	mu      sync.Mutex
	method  string
	path    string
	reqBody string
	calls   int
}

func (r *replyRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.method = req.Method
	r.path = req.URL.Path
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		r.reqBody = string(b)
	}
	code := r.code
	if code == 0 {
		code = 200
	}
	body := r.respBody
	if code != 200 {
		body = r.errBody
	}
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func TestReply_postsReplyTextAndReturnsRawBody(t *testing.T) {
	rt := &replyRT{respBody: `{"result":{"replyText":"thanks","lastEdited":{"seconds":"1700000000"}}}`}
	hc := &http.Client{Transport: rt}

	raw, err := Reply(context.Background(), hc, "com.example.app", "gp:AOqpT123", "thanks")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}

	if rt.method != http.MethodPost {
		t.Errorf("method = %q, want POST", rt.method)
	}
	// The reviewId's own colon and the :reply custom-method colon both stay
	// literal in the path.
	wantPath := "/androidpublisher/v3/applications/com.example.app/reviews/gp:AOqpT123:reply"
	if rt.path != wantPath {
		t.Errorf("path = %q, want %q", rt.path, wantPath)
	}
	// The body carries the reply under the API's replyText field.
	var sent struct {
		ReplyText string `json:"replyText"`
	}
	if err := json.Unmarshal([]byte(rt.reqBody), &sent); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, rt.reqBody)
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
			rt := &replyRT{code: tc.code, errBody: `{"error":{"code":` + strconv.Itoa(tc.code) + `,"message":"nope"}}`}
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
	rt := &loopingRT{t: t}
	hc := &http.Client{Transport: rt}

	_, err := List(context.Background(), hc, "com.example.app")
	if err == nil {
		t.Fatal("expected an error when the API repeats a pagination token, got nil")
	}
	// First call yields LOOP (new), second call repeats LOOP → detected.
	if rt.calls != 2 {
		t.Errorf("expected exactly 2 calls before detecting the loop, got %d", rt.calls)
	}
}

func TestList_autoPaginates(t *testing.T) {
	page1 := `{"reviews":[{"reviewId":"r1","comments":[{"userComment":{"text":"a","starRating":5,"reviewerLanguage":"en"}}]}],"tokenPagination":{"nextPageToken":"PAGE2"}}`
	page2 := `{"reviews":[{"reviewId":"r2","comments":[{"userComment":{"text":"b","starRating":3,"reviewerLanguage":"en"}}]}],"tokenPagination":{"nextPageToken":"PAGE3"}}`
	page3 := `{"reviews":[{"reviewId":"r3","comments":[{"userComment":{"text":"c","starRating":1,"reviewerLanguage":"en"}}]}]}` // no nextPageToken → stop
	rt := &reviewsRT{pages: []string{page1, page2, page3}}
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
	if len(rt.calls) != 3 {
		t.Fatalf("made %d calls, want 3: %v", len(rt.calls), rt.calls)
	}
	if got := rt.calls[1]; !strings.Contains(got, "token=PAGE2") {
		t.Errorf("2nd call = %q, want token=PAGE2", got)
	}
	if got := rt.calls[2]; !strings.Contains(got, "token=PAGE3") {
		t.Errorf("3rd call = %q, want token=PAGE3", got)
	}
}

func TestList_singlePage(t *testing.T) {
	body := `{"reviews":[
		{"reviewId":"r1","comments":[{"userComment":{"text":"Great app\nsecond line","starRating":5,"reviewerLanguage":"en","lastModified":{"seconds":"1700000000","nanos":0}}}]},
		{"reviewId":"r2","comments":[{"userComment":{"text":"Bad","starRating":1,"reviewerLanguage":"fr-FR","lastModified":{"seconds":"1700000100"}}}]}
	]}`
	rt := &reviewsRT{pages: []string{body}}
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
	if len(rt.calls) != 1 || rt.calls[0] != wantCall {
		t.Errorf("calls = %v, want exactly [%q]", rt.calls, wantCall)
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
