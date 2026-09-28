package list_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd/gamescmdtest"
	listcmd "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/list"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRun_nextPageTokenNote asserts a page carrying nextPageToken tells the
// operator on stderr how to fetch the next one: a first page in table or
// markdown would otherwise read as the whole list. The last page says nothing.
func TestRun_nextPageTokenNote(t *testing.T) {
	for _, c := range []struct {
		body, want string
	}{
		{`{"items":[{"id":"l1"}],"nextPageToken":"tok-2"}`, "--page-token tok-2"},
		{listBody, ""},
	} {
		rc := gamescmdtest.NewRC(t, testkit.NewFake(testkit.Any(http.StatusOK, c.body)))
		var stderr bytes.Buffer
		rc.Stderr = &stderr
		if _, err := listcmd.Run(rc, listcmd.Input{ApplicationID: "12345"}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if c.want == "" && stderr.Len() != 0 {
			t.Errorf("the last page must not claim more: stderr %q", stderr.String())
		}
		if c.want != "" && !strings.Contains(stderr.String(), c.want) {
			t.Errorf("stderr %q should carry %q", stderr.String(), c.want)
		}
	}
}
