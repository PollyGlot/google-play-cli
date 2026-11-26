package transport

import (
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
	gen      uint64
	captured []byte
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
	if req.Body == nil || req.Body == http.NoBody {
		return t.inner.RoundTrip(req)
	}
	observed := req.Clone(req.Context())
	observed.Body = t.obs.capture(req.Body)
	if req.GetBody != nil {
		observed.GetBody = func() (io.ReadCloser, error) {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			return t.obs.capture(body), nil
		}
	}
	return t.inner.RoundTrip(observed)
}

type capturingBody struct {
	io.ReadCloser
	obs *ScopeObserver
	gen uint64
}

func (b *capturingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.obs.record(b.gen, p[:n])
	}
	return n, err
}

func (o *ScopeObserver) capture(body io.ReadCloser) io.ReadCloser {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.gen++
	o.captured = nil
	return &capturingBody{ReadCloser: body, obs: o, gen: o.gen}
}

func (o *ScopeObserver) record(gen uint64, p []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	room := maxObservedBody - len(o.captured)
	if gen != o.gen || room <= 0 {
		return
	}
	o.captured = append(o.captured, p[:min(len(p), room)]...)
}

func (o *ScopeObserver) Scopes() []string {
	o.mu.Lock()
	captured := string(o.captured)
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
