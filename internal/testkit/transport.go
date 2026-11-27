package testkit

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const TokenURL = "https://oauth2.googleapis.com/token"

const tokenBody = `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`

func IsTokenRequest(r *http.Request) bool {
	return r.URL.Scheme+"://"+r.URL.Host+r.URL.Path == TokenURL && r.URL.RawQuery == ""
}

func TokenResponse(r *http.Request) (*http.Response, bool) {
	if !IsTokenRequest(r) {
		return nil, false
	}
	if r.Body != nil {
		body := ReadBody(r)
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	return Response(http.StatusOK, tokenBody), true
}

func Response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type Call struct {
	Method        string
	Host          string
	Path          string
	Query         string
	Header        http.Header
	Body          []byte
	URL           string
	ContentLength int64
	Replayable    bool
	Replayed      []byte

	Reply http.Header
}

type Responder func(c Call) (status int, body string, ok bool)

type Fake struct {
	responders []Responder

	mu       sync.Mutex
	calls    []Call
	tokens   int
	allowed  map[string]bool
	collect  bool
	refusals []string
}

func NewFake(responders ...Responder) *Fake {
	return &Fake{responders: responders, allowed: map[string]bool{}}
}

func (f *Fake) AllowInvalid(rules ...string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	allowList(f.allowed, rules)
	return f
}

func (f *Fake) CollectRefusals() *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.collect = true
	return f
}

func (f *Fake) Refusals() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.refusals...)
}

func (f *Fake) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	if resp, ok := TokenResponse(req); ok {
		f.mu.Lock()
		f.tokens++
		f.mu.Unlock()
		return resp, nil
	}
	c, err := f.record(req)
	if err != nil {
		return nil, err
	}
	for _, r := range f.responders {
		if status, b, ok := r(c); ok {
			if status == 0 {
				status = http.StatusOK
			}
			resp := Response(status, b)
			for k, v := range c.Reply {
				resp.Header[k] = v
			}
			return resp, nil
		}
	}
	return nil, fmt.Errorf("testkit: no responder for %s %s", req.Method, req.URL.Redacted())
}

func (f *Fake) Record(req *http.Request) error {
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	_, err := f.record(req)
	return err
}

func (f *Fake) record(req *http.Request) (Call, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return Call{}, fmt.Errorf("testkit: read request body: %w", err)
		}
		body = b
	}
	replayed, err := replay(req)
	if err != nil {
		return Call{}, err
	}
	c := Call{
		Method: req.Method,
		Host:   req.URL.Host,
		Path:   req.URL.Path,
		Query:  req.URL.RawQuery,
		Header: req.Header.Clone(),
		Body:   body,

		URL:           req.URL.String(),
		ContentLength: req.ContentLength,
		Replayable:    req.Body == nil || req.Body == http.NoBody || req.GetBody != nil,
		Replayed:      replayed,
		Reply:         http.Header{},
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	why := refusal(c, f.allowed)
	collect := f.collect
	if why != "" && collect {
		f.refusals = append(f.refusals, why)
	}
	f.mu.Unlock()
	if why != "" {
		if collect {
			return Call{}, fmt.Errorf("testkit: body refused by the constraints table")
		}
		panic(why)
	}
	return c, nil
}

func replay(req *http.Request) ([]byte, error) {
	if req.GetBody == nil {
		return nil, nil
	}
	rc, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("testkit: replay request body: %w", err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("testkit: read replayed request body: %w", err)
	}
	return b, nil
}

func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

func (f *Fake) TokenExchanges() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokens
}

func (f *Fake) Wrote() bool {
	for _, c := range f.Calls() {
		switch c.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			return true
		}
	}
	return false
}

func Any(status int, body string) Responder {
	return func(Call) (int, string, bool) { return status, body, true }
}

func ReplyHeader(r Responder, key string, value func(c Call, status int) string) Responder {
	return func(c Call) (int, string, bool) {
		status, body, ok := r(c)
		if ok && c.Reply != nil {
			sent := status
			if sent == 0 {
				sent = http.StatusOK
			}
			if v := value(c, sent); v != "" {
				c.Reply.Set(key, v)
			}
		}
		return status, body, ok
	}
}

func Refuse(t testing.TB, why string) Responder {
	t.Helper()
	return func(c Call) (int, string, bool) {
		t.Errorf("unexpected request%s: %s %s", why, c.Method, c.Path)
		return 0, "", false
	}
}
