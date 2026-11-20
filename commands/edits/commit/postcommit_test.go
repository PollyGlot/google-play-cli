package commit_test

import (
	"errors"
	"strings"
	"testing"

	commitcmd "github.com/PollyGlot/google-play-cli/commands/edits/commit"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type failRemoveFS struct{ config.FS }

func (failRemoveFS) Remove(string) error { return errors.New("permission denied") }

func TestRun_commitSucceedsButPinClearFails_saysTheChangeIsLive(t *testing.T) {
	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if strings.HasSuffix(c.Path, "/edits/edit-9:commit") {
			return 200, `{"id":"edit-9","expiryTimeSeconds":"0"}`, true
		}
		return 0, "", false
	})
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}
	rc.FS = failRemoveFS{config.OSFS{}}

	_, err := commitcmd.Run(rc, commitcmd.Input{Package: pkg})
	if err == nil {
		t.Fatal("expected the pin-clear failure to surface")
	}
	msg := err.Error()
	for _, want := range []string{"committed explicit edit edit-9", editpin.Path(gplayDir, pkg), "remove it manually", "permission denied"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
	if calls := fake.Calls(); len(calls) != 1 {
		t.Errorf("calls = %d, want exactly the one commit (no retry, no discard): %+v", len(calls), calls)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); !ok {
		t.Error("the pin was removed although Remove failed; the test did not reach the branch")
	}
}
