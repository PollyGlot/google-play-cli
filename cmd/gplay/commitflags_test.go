package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestCommitFlags_onEveryEditCommittingLeaf pins where the #598 opt-ins live:
// on `edits commit` and on every write command that commits its own implicit
// Edit. A leaf missing them would silently cancel a pending review with no way
// to opt out. The reverse guard catches the next Edit-committing command: every
// leaf with --keep-edit-on-failure commits an Edit, so it must be listed here.
//
// Paths are split args so the verb gate (#168) stays green on this file.
func TestCommitFlags_onEveryEditCommittingLeaf(t *testing.T) {
	committing := [][]string{
		{"edits", "commit"},
		{"releases", "upload"},
		{"releases", "promote"},
		{"releases", "rollout"},
		{"releases", "halt"},
		{"releases", "resume"},
		{"releases", "complete"},
		{"releases", "mappings", "upload"},
		{"releases", "expansion-files", "upload"},
		{"releases", "expansion-files", "set"},
		{"tracks", "create"},
		{"testers", "set"},
		{"apps", "details", "set"},
		{"metadata", "apply"},
		{"metadata", "images", "apply"},
	}
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	listed := map[string]bool{}
	for _, path := range committing {
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil || cmd.Name() != path[len(path)-1] {
			t.Errorf("%v resolves to no leaf (renamed or removed?)", path)
			continue
		}
		listed[cmd.CommandPath()] = true
		for _, name := range []string{commitflags.ChangesInReview, commitflags.ChangesNotSentForReview} {
			f := cmd.Flags().Lookup(name)
			if f == nil {
				t.Errorf("%q lacks --%s", cmd.CommandPath(), name)
				continue
			}
			// Frozen like their leaves (they mirror Google's parameters 1:1),
			// so no [experimental] label may creep back into the help.
			if strings.Contains(f.Usage, "[experimental]") {
				t.Errorf("%q --%s usage %q carries an [experimental] label; these flags are frozen", cmd.CommandPath(), name, f.Usage)
			}
		}
	}

	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Runnable() && c.Flags().Lookup("keep-edit-on-failure") != nil && !listed[c.CommandPath()] {
			t.Errorf("%q commits an Edit (it has --keep-edit-on-failure) but is not in the commit-flags list", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
