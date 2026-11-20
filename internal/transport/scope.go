package transport

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const maxObservedBody = 64 * 1024

type ScopeObserver struct {
	mu       sync.Mutex
	captured string
}

type observerTransport struct {
	inner http.RoundTripper
	obs   *ScopeObserver
}

func WithScopeObserver(inner http.RoundTripper) (http.RoundTripper, *ScopeObserver) {
	if inner == nil {
		inner = http.DefaultTransport
	}
	obs := &ScopeObserver{}
	return &observerTransport{inner: inner, obs: obs}, obs
}

func (t *observerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		buf, err := io.ReadAll(req.Body)
		if err == nil {
			observed := buf
			if len(observed) > maxObservedBody {
				observed = observed[:maxObservedBody]
			}
			t.obs.mu.Lock()
			t.obs.captured = string(observed)
			t.obs.mu.Unlock()
			req.Body = io.NopCloser(bytes.NewReader(buf))
			req.ContentLength = int64(len(buf))
		}
	}
	return t.inner.RoundTrip(req)
}

func (o *ScopeObserver) Scopes() []string {
	o.mu.Lock()
	captured := o.captured
	o.mu.Unlock()
	if captured == "" {
		return nil
	}
	values, err := url.ParseQuery(captured)
	if err != nil {
		return nil
	}
	raw := values.Get("scope")
	if raw == "" {
		return scopesFromAssertion(values.Get("assertion"))
	}
	return strings.Fields(raw)
}

func scopesFromAssertion(jwt string) []string {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil
	}
	const key = `"scope":"`
	idx := strings.Index(payload, key)
	if idx < 0 {
		return nil
	}
	rest := payload[idx+len(key):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return nil
	}
	return strings.Fields(rest[:end])
}

func base64URLDecode(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
