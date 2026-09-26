package testkit_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// send issues one request through hc and returns the status and body.
func send(t *testing.T, hc *http.Client, method, url, contentType string, body io.Reader) (int, string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

func TestWire_recordsRequestsAsTheServerReadsThem(t *testing.T) {
	w := testkit.NewWire(t, func(c testkit.Call) (int, string, bool) {
		return http.StatusCreated, `{"ok":true}`, c.Method == http.MethodPost
	})
	hc := w.Client()

	status, body, err := send(t, hc, http.MethodPost, "https://androidpublisher.googleapis.com/a/b?x=1", "application/json", strings.NewReader(`{"k":"v"}`))
	if err != nil || status != http.StatusCreated || body != `{"ok":true}` {
		t.Errorf("POST = %d %s %v, want the responder's 201", status, body, err)
	}
	if status, _, err = send(t, hc, http.MethodGet, "https://upload.example.com/c", "", nil); err != nil || status != 599 {
		t.Errorf("unclaimed GET = %d %v, want 599", status, err)
	}
	if _, _, err = send(t, hc, http.MethodPost, "https://androidpublisher.googleapis.com/d", "image/png", bytes.NewReader([]byte{0xff, 0xfe})); err != nil {
		t.Fatalf("POST binary: %v", err)
	}

	tr := string(w.Transcript())
	for _, want := range []string{
		"POST https://androidpublisher.googleapis.com/a/b?x=1\n",
		"Content-Length: 9\n",
		"Content-Type: application/json\n",
		"\n{\"k\":\"v\"}\n",
		"GET https://upload.example.com/c\n",
		"<2 bytes, sha256 ",
	} {
		if !strings.Contains(tr, want) {
			t.Errorf("transcript lacks %q:\n%s", want, tr)
		}
	}
}

func TestRoundTripFunc_isATransport(t *testing.T) {
	boom := errors.New("boom")
	hc := &http.Client{Transport: testkit.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := testkit.TokenResponse(r); ok {
			return resp, nil
		}
		return nil, boom
	})}
	if status, _, err := send(t, hc, http.MethodPost, testkit.TokenURL, "", nil); err != nil || status != http.StatusOK {
		t.Fatalf("token POST = %d %v, want 200", status, err)
	}
	if _, _, err := send(t, hc, http.MethodGet, "https://androidpublisher.googleapis.com/x", "", nil); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the closure's error", err)
	}
}
