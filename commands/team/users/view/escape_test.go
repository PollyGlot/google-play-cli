package view_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestHumanViewsStripTerminalEscapes(t *testing.T) {
	user := strings.NewReplacer(
		`"PENDING_SIGNUP"`, `"PENDING_SIGNUP`+outputtest.HostileJSON+`"`,
		`"com.example.foo"`, `"com.example.foo`+outputtest.HostileJSON+`"`,
	).Replace(aliceUser)
	r, err := run(t, usersBody(user), "alice@example.com")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for f, got := range outputtest.RenderHuman(t, r) {
		if !strings.Contains(got, "PENDING_SIGNUP") || !strings.Contains(got, "com.example.foo") {
			t.Errorf("--output %s lost the visible text:\n%s", f, got)
		}
	}
	if js := renderJSON(t, r); !strings.Contains(js, outputtest.HostileJSON) {
		t.Errorf("json must keep the API bytes verbatim, got:\n%s", js)
	}
}
