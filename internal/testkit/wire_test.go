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

type coded struct{ code int }

func (c coded) Error() string { return "refused" }
func (c coded) ExitCode() int { return c.code }

// Wire must tell apart every property of a request that reaches the network
// or decides a replay: escaping, headers, declared length, a re-openable body.
func TestWire_rendersWhatReachesTheNetwork(t *testing.T) {
	send := func(hc *http.Client) (any, error) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.test/a%2Fb?x=1", bytes.NewReader([]byte(`{"k":1}`)))
		req.Header.Set("Content-Type", "application/json")
		if _, err := hc.Do(req); err != nil {
			return nil, err
		}
		stream, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, "https://api.test/up", io.NopCloser(strings.NewReader("raw")))
		stream.ContentLength = 3
		_, err := hc.Do(stream)
		return rawJSON(`{"done":true}`), err
	}
	got := testkit.Exchange("post", send, testkit.Any(http.StatusOK, `{}`))
	want := "## post\n" +
		"POST https://api.test/a%2Fb?x=1\nContent-Type: application/json\ncontent-length=7 replayable=true\n{\"k\":1}\n" +
		"PUT https://api.test/up\ncontent-length=3 replayable=false\nraw\n" +
		"=> ok {\"done\":true}\n\n"
	if got != want {
		t.Errorf("Exchange =\n%s\nwant\n%s", got, want)
	}
}

type rawJSON string

func (j rawJSON) MarshalJSON() ([]byte, error) { return []byte(j), nil }

func TestExchange_recordsTheExitCodeOfAnError(t *testing.T) {
	got := testkit.Exchange("refused", func(*http.Client) (any, error) { return nil, coded{11} })
	if !strings.HasSuffix(got, "=> error (exit 11): refused\n\n") {
		t.Errorf("Exchange = %q", got)
	}
	got = testkit.Exchange("plain", func(*http.Client) (any, error) { return nil, errors.New("boom") })
	if !strings.HasSuffix(got, "=> error (exit -1): boom\n\n") {
		t.Errorf("Exchange = %q", got)
	}
}

func TestSequence_servesPagesThenRepeatsTheLast(t *testing.T) {
	seq := testkit.Sequence("a", "b")
	var got []string
	for range 3 {
		_, body, ok := seq(testkit.Call{})
		if !ok {
			t.Fatal("Sequence must claim every call")
		}
		got = append(got, body)
	}
	if strings.Join(got, ",") != "a,b,b" {
		t.Errorf("bodies = %v, want a,b,b", got)
	}
}

func TestRoundTripFunc_isATransport(t *testing.T) {
	var rt http.RoundTripper = testkit.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := testkit.TokenResponse(r); ok {
			return resp, nil
		}
		return testkit.Response(http.StatusTeapot, `{}`), nil
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.test/", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusTeapot {
		t.Fatalf("RoundTrip = %v, %v", resp, err)
	}
}
