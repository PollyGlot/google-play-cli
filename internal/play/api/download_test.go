package api_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

var mDownload = apiregistry.MustResolve("androidpublisher.generatedapks.download")

func downloadCall() api.Call {
	return api.Call{
		Method: mDownload, Target: "com.example.app",
		Params: map[string]string{"packageName": "com.example.app", "versionCode": "42", "downloadId": "d1"},
		Query:  map[string][]string{"alt": {"media"}},
	}
}

// A media payload has no size cap: Download streams past the 4 MiB limit Do
// enforces on a JSON body, and reports the bytes it wrote.
func TestDownload_streamsPastTheJSONLimit(t *testing.T) {
	payload := strings.Repeat("x", api.MaxAPISuccessBodyRead+10)
	fake := testkit.NewFake(testkit.Any(http.StatusOK, payload))
	var got bytes.Buffer
	n, err := api.Download(context.Background(), &http.Client{Transport: fake}, downloadCall(), &got)
	if err != nil || n != int64(len(payload)) || got.String() != payload {
		t.Fatalf("Download = %d, %v (wrote %d bytes), want the whole %d-byte payload", n, err, got.Len(), len(payload))
	}
	c := fake.Calls()[0]
	if c.Method != http.MethodGet || !strings.HasSuffix(c.URL, "/generatedApks/42/downloads/d1:download?alt=media") {
		t.Errorf("request = %s %s", c.Method, c.URL)
	}
}

func TestDownload_refusalCarriesTheEnvelope(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusNotFound, `{"error":{"message":"no such artifact","errors":[{"reason":"notFound"}]}}`))
	var got bytes.Buffer
	_, err := api.Download(context.Background(), &http.Client{Transport: fake}, downloadCall(), &got)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.StatusCode != 404 || ae.Message != "no such artifact" || ae.Operation != "generatedapks.download" {
		t.Fatalf("err = %#v, want the 404 envelope", err)
	}
	if got.Len() != 0 {
		t.Errorf("an error body must not reach the destination, got %q", got.String())
	}
}

// A body cut by the network is the retry-safe bucket, as in Do; a writer that
// refuses the bytes keeps its own error reachable and the answer's status.
func TestDownload_tellsACutBodyFromARefusingWriter(t *testing.T) {
	cut := testkit.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		resp := testkit.Response(http.StatusOK, "")
		resp.Body = io.NopCloser(io.MultiReader(strings.NewReader("part"), cutReader{io.ErrUnexpectedEOF}))
		return resp, nil
	})
	var got bytes.Buffer
	n, err := api.Download(context.Background(), &http.Client{Transport: cut}, downloadCall(), &got)
	if n != 4 || exit.For(err) != 50 || !strings.Contains(err.Error(), "stream response body") {
		t.Errorf("cut body: n=%d exit=%d err=%v, want 4 bytes and exit 50", n, exit.For(err), err)
	}

	full := errors.New("disk full")
	fake := testkit.NewFake(testkit.Any(http.StatusOK, "payload"))
	_, err = api.Download(context.Background(), &http.Client{Transport: fake}, downloadCall(), failWriter{full})
	var ae *api.Error
	if !errors.Is(err, full) || !errors.As(err, &ae) || ae.StatusCode != 200 {
		t.Errorf("refusing writer: err = %#v, want the writer's error with status 200", err)
	}
}

// Call.URL sends a URL an API response handed back verbatim, with GET, and
// ignores the Method template, its Params and any Query.
func TestDo_handedBackURLIsSentVerbatim(t *testing.T) {
	const u = "https://play-lh.googleusercontent.com/abc=w512-h512?x=1"
	fake := testkit.NewFake(testkit.Any(http.StatusOK, "img"))
	var got bytes.Buffer
	_, err := api.Download(context.Background(), &http.Client{Transport: fake},
		api.Call{URL: u, Op: "images.download", Query: map[string][]string{"ignored": {"1"}}}, &got)
	if err != nil {
		t.Fatal(err)
	}
	if c := fake.Calls()[0]; c.Method != http.MethodGet || c.URL != u {
		t.Errorf("request = %s %s, want GET %s", c.Method, c.URL, u)
	}
}

type cutReader struct{ err error }

func (r cutReader) Read([]byte) (int, error) { return 0, r.err }

type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }
