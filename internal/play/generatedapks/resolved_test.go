package generatedapks_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/generatedapks"
)

func pinned() *testkit.Fake { return testkit.NewFake(testkit.Any(http.StatusOK, `{}`)) }

func first(f *testkit.Fake) testkit.Call {
	if calls := f.Calls(); len(calls) > 0 {
		return calls[0]
	}
	return testkit.Call{}
}

const base = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app"

func TestResolvedURLsUnchanged(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		call func(*http.Client) error
		verb string
		want string
	}{
		{
			name: "generatedapks.list",
			call: func(hc *http.Client) error {
				_, _, err := generatedapks.List(ctx, hc, "com.example.app", 42)
				return err
			},
			verb: http.MethodGet,
			want: base + "/generatedApks/42",
		},
		{
			name: "generatedapks.download",
			call: func(hc *http.Client) error {
				_, err := generatedapks.Download(ctx, hc, "com.example.app", 42, "dl-1", io.Discard)
				return err
			},
			verb: http.MethodGet,
			want: base + "/generatedApks/42/downloads/dl-1:download?alt=media",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := pinned()
			if err := tc.call(&http.Client{Transport: rt}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if first(rt).URL != tc.want {
				t.Errorf("URL = %q, want %q", first(rt).URL, tc.want)
			}
			if first(rt).Method != tc.verb {
				t.Errorf("verb = %q, want %q", first(rt).Method, tc.verb)
			}
		})
	}
}

func TestEmptyDownloadIDFailsBeforeTheWire(t *testing.T) {
	rt := pinned()
	_, err := generatedapks.Download(context.Background(), &http.Client{Transport: rt}, "com.example.app", 42, "", io.Discard)
	if err == nil {
		t.Fatal("Download with an empty download id succeeded, want an error")
	}
	if first(rt).URL != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", first(rt).URL)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if !strings.Contains(apiErr.Message, `"downloadId"`) {
		t.Errorf("message = %q, want it to name the downloadId parameter", apiErr.Message)
	}
}
