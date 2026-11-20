package teamtest

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
	"golang.org/x/oauth2"
)

const DeveloperID = "4900000000000000000"

type Call = testkit.Call

type Responder = testkit.Responder

type RT = testkit.Fake

func New(responders ...Responder) *RT {
	return testkit.NewFake(append(responders, testkit.Any(200, ""))...)
}

func UsersList(body string) Responder {
	return func(c Call) (int, string, bool) {
		if c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/users") {
			return 200, body, true
		}
		return 0, "", false
	}
}

func Pages(bodies ...string) Responder {
	var i int
	var mu sync.Mutex
	return func(c Call) (int, string, bool) {
		if c.Method != http.MethodGet || !strings.HasSuffix(c.Path, "/users") {
			return 0, "", false
		}
		mu.Lock()
		defer mu.Unlock()
		b := bodies[len(bodies)-1]
		if i < len(bodies) {
			b = bodies[i]
		}
		i++
		return 200, b, true
	}
}

func Fail(method, pathSuffix string, status int, body string) Responder {
	return func(c Call) (int, string, bool) {
		if c.Method == method && strings.HasSuffix(c.Path, pathSuffix) {
			return status, body, true
		}
		return 0, "", false
	}
}

func NewRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.AccountName = "ci-bot"
	rc.Resolved = &config.Resolved{DeveloperID: DeveloperID}
	return rc
}
