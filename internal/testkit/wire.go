package testkit

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

type Wire struct {
	t          testing.TB
	srv        *httptest.Server
	client     *http.Client
	responders []Responder

	mu      sync.Mutex
	lines   []string
	allowed map[string]bool
}

func NewWire(t testing.TB, responders ...Responder) *Wire {
	t.Helper()
	w := &Wire{t: t, responders: responders, allowed: map[string]bool{}}
	w.srv = httptest.NewTLSServer(http.HandlerFunc(w.serve))
	t.Cleanup(w.srv.Close)
	addr := w.srv.Listener.Addr().String()
	tr := w.srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	tr.DialTLSContext = nil
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server, local only
	w.client = w.srv.Client()
	w.client.Transport = tr
	return w
}

func (w *Wire) AllowInvalid(rules ...string) *Wire {
	w.mu.Lock()
	defer w.mu.Unlock()
	allowList(w.allowed, rules)
	return w
}

func (w *Wire) Client() *http.Client { return w.client }

func (w *Wire) Transcript() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return []byte(strings.Join(w.lines, "\n"))
}

func (w *Wire) serve(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s https://%s%s\n", r.Method, r.Host, r.RequestURI)
	h := r.Header.Clone()
	if len(r.TransferEncoding) > 0 {
		h.Set("Transfer-Encoding", strings.Join(r.TransferEncoding, ","))
	}
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Fprintf(&b, "%s: %s\n", k, strings.Join(h[k], ","))
	}
	switch {
	case len(body) == 0:
	case utf8.Valid(body):
		fmt.Fprintf(&b, "\n%s\n", body)
	default:
		fmt.Fprintf(&b, "\n<%d bytes, sha256 %x>\n", len(body), sha256.Sum256(body))
	}
	c := Call{Method: r.Method, Host: r.Host, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body, URL: "https://" + r.Host + r.RequestURI, ContentLength: r.ContentLength, Reply: http.Header{}}
	w.mu.Lock()
	w.lines = append(w.lines, b.String())
	why := refusal(c, w.allowed)
	w.mu.Unlock()
	if why != "" {
		w.t.Error(why)
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(rw, `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT"}}`)
		return
	}
	for _, resp := range w.responders {
		if status, out, ok := resp(c); ok {
			if status == 0 {
				status = http.StatusOK
			}
			for k, v := range c.Reply {
				rw.Header()[k] = v
			}
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(status)
			_, _ = io.WriteString(rw, out)
			return
		}
	}
	http.Error(rw, "testkit: no responder for "+r.Method+" "+r.URL.Path, 599)
}
