package testkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

func ReadBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	b, _ := io.ReadAll(r.Body)
	return b
}

func renderCalls(calls []Call) string {
	var b strings.Builder
	for _, c := range calls {
		fmt.Fprintf(&b, "%s %s\n", c.Method, c.URL)
		keys := make([]string, 0, len(c.Header))
		for k := range c.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s: %s\n", k, strings.Join(c.Header[k], ", "))
		}
		fmt.Fprintf(&b, "content-length=%d replayable=%t\n", c.ContentLength, c.Replayable)
		if len(c.Body) > 0 {
			fmt.Fprintf(&b, "%s\n", c.Body)
		}
	}
	return b.String()
}

func Exchange(name string, send func(hc *http.Client) (any, error), responders ...Responder) string {
	fake := NewFake(responders...)
	got, err := send(&http.Client{Transport: fake})
	out := "## " + name + "\n" + renderCalls(fake.Calls())
	if err == nil {
		return out + "=> ok " + render(got) + "\n\n"
	}
	code := -1
	var coder interface{ ExitCode() int }
	if errors.As(err, &coder) {
		code = coder.ExitCode()
	}
	return out + fmt.Sprintf("=> error (exit %d): %v\n\n", code, err)
}

func render(v any) string {
	switch b := v.(type) {
	case []byte:
		return string(b)
	case json.RawMessage:
		return string(b)
	}
	if j, err := json.Marshal(v); err == nil {
		return string(j)
	}
	return fmt.Sprintf("%+v", v)
}

type Send struct {
	Name string
	Fn   func(hc *http.Client) (any, error)
}

type Answer struct {
	Name       string
	Responders []Responder
}

var (
	AnswerOK        = Answer{Name: "ok", Responders: []Responder{Any(http.StatusOK, `{}`)}}
	AnswerDenied    = Answer{Name: "denied", Responders: []Responder{Any(http.StatusForbidden, `{"error":{"code":403,"message":"The caller does not have permission","errors":[{"reason":"forbidden"}]}}`)}}
	AnswerMalformed = Answer{Name: "malformed", Responders: []Responder{Any(http.StatusOK, `not json`)}}
)

func Exchanges(sends []Send, answers ...Answer) string {
	var b strings.Builder
	for _, s := range sends {
		for _, a := range answers {
			b.WriteString(Exchange(s.Name+" / "+a.Name, s.Fn, a.Responders...))
		}
	}
	return b.String()
}

func Sequence(bodies ...string) Responder {
	var (
		mu sync.Mutex
		i  int
	)
	return func(Call) (int, string, bool) {
		mu.Lock()
		defer mu.Unlock()
		b := bodies[len(bodies)-1]
		if i < len(bodies) {
			b = bodies[i]
		}
		i++
		return http.StatusOK, b, true
	}
}
