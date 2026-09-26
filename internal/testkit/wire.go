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

// Wire records requests as a server reads them off the connection, for the
// tests that pin what a module sends byte for byte: the request line, every
// header net/http put on the wire (Content-Length and Transfer-Encoding
// included, which an in-process RoundTripper never sees) and the body. The
// client it hands out dials a local TLS server whatever the URL's host, so
// the registry's real https URLs are exercised unchanged.
type Wire struct {
	srv        *httptest.Server
	client     *http.Client
	responders []Responder

	mu    sync.Mutex
	lines []string
}

// NewWire starts a Wire whose server answers each request from the first
// matching responder (a status of 0 means 200). A request no responder claims
// gets a 599, so the transcript still shows it and the call under test fails.
// The server stops when the test ends.
func NewWire(t testing.TB, responders ...Responder) *Wire {
	t.Helper()
	w := &Wire{responders: responders}
	w.srv = httptest.NewTLSServer(http.HandlerFunc(w.serve))
	t.Cleanup(w.srv.Close)
	addr := w.srv.Listener.Addr().String()
	tr := w.srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	tr.DialTLSContext = nil
	// The server's certificate names 127.0.0.1, not the Google hosts the
	// requests address; what is under test is the request, not the TLS.
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server, local only
	// Reuse the server's own client (its Close then also drops our idle
	// connections) rather than building one: testkit is not a _test file.
	w.client = w.srv.Client()
	w.client.Transport = tr
	return w
}

// Client returns the client whose every request lands on the Wire.
func (w *Wire) Client() *http.Client { return w.client }

// Transcript returns the recorded requests in arrival order, one block per
// request separated by a blank line: the request line with the Host, the
// headers sorted by name, then the body.
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
	// The server moves Transfer-Encoding out of the header map; put it back so
	// the transcript shows how the body was framed (Content-Length stays).
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
		// Media bytes: their digest pins them as surely and keeps the
		// golden reviewable.
		fmt.Fprintf(&b, "\n<%d bytes, sha256 %x>\n", len(body), sha256.Sum256(body))
	}
	w.mu.Lock()
	w.lines = append(w.lines, b.String())
	w.mu.Unlock()

	c := Call{Method: r.Method, Host: r.Host, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body, Reply: http.Header{}}
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
