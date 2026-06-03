package main

import (
	"runtime/debug"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestVerbVocabulary_canonicalNames asserts the ADR-0019 verb renames at
// the user-facing command-tree level: the canonical names resolve to a
// command, and the pre-rename names are gone (a hard rename leaves no
// alias, so the old verb is not a registered subcommand). This is the
// contract guard for the verb audit (#98) — one e2e case per rename,
// exercised through the real cobra tree via Command.Find, which resolves
// commands without executing them or touching auth/network.
func TestVerbVocabulary_canonicalNames(t *testing.T) {
	cases := []struct {
		name     string
		path     []string
		wantGone bool // true: the old name must NOT resolve (hard-renamed away)
	}{
		// #163 apps info → apps view
		{"apps view resolves", []string{"apps", "view"}, false},
		{"apps info is gone", []string{"apps", "info"}, true},
		// #164 tracks status → tracks view
		{"tracks view resolves", []string{"tracks", "view"}, false},
		{"tracks status is gone", []string{"tracks", "status"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			cmd, rest, err := root.Find(tc.path)
			if err != nil {
				t.Fatalf("Find(%v): unexpected error: %v", tc.path, err)
			}
			if tc.wantGone {
				// The old verb must stay unconsumed (Find stops at the parent
				// group and leaves the unknown name in rest). If rest is empty
				// the old name still resolved — the hard rename is incomplete.
				if len(rest) == 0 {
					t.Fatalf("%v: expected the old name to be unresolved (a hard rename leaves no alias), but it resolved to %q", tc.path, cmd.CommandPath())
				}
				return
			}
			// The canonical name must resolve fully: nothing left unconsumed,
			// and the resolved leaf carries the final path element as its name.
			if len(rest) != 0 {
				t.Fatalf("%v: expected to resolve fully, but %v was left unconsumed (resolved %q)", tc.path, rest, cmd.CommandPath())
			}
			if want := tc.path[len(tc.path)-1]; cmd.Name() != want {
				t.Fatalf("%v: resolved to leaf %q, want %q", tc.path, cmd.Name(), want)
			}
		})
	}
}

func TestRootCmd_persistentFlags_serviceAccountAccountAndVerbose(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	for _, name := range []string{"service-account", "account", "verbose"} {
		f := root.PersistentFlags().Lookup(name)
		if f == nil {
			t.Errorf("root command missing persistent --%s flag", name)
		}
	}
	// -v is the documented shorthand for --verbose (docs/DESIGN.md §8).
	if f := root.PersistentFlags().ShorthandLookup("v"); f == nil || f.Name != "verbose" {
		t.Errorf("root command missing -v shorthand wired to --verbose; got %v", f)
	}
}

func TestResolveVersion(t *testing.T) {
	buildInfo := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.0-alpha.1"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-05-22T12:00:00Z"},
			{Key: "GOOS", Value: "linux"},
		},
	}

	tests := []struct {
		name                                 string
		ldVersion, ldCommit, ldDate          string
		info                                 *debug.BuildInfo
		infoOK                               bool
		wantVersion, wantCommit, wantDateStr string
	}{
		{
			name:      "ldflags present — keep them, ignore BuildInfo",
			ldVersion: "v9.9.9", ldCommit: "deadbeef", ldDate: "2030-01-01",
			info: buildInfo, infoOK: true,
			wantVersion: "v9.9.9", wantCommit: "deadbeef", wantDateStr: "2030-01-01",
		},
		{
			name:      "ldflags default + BuildInfo — fall back to BuildInfo",
			ldVersion: "dev", ldCommit: "none", ldDate: "unknown",
			info: buildInfo, infoOK: true,
			wantVersion: "v0.1.0-alpha.1", wantCommit: "abc123", wantDateStr: "2026-05-22T12:00:00Z",
		},
		{
			name:      "ldflags default + no BuildInfo — keep defaults",
			ldVersion: "dev", ldCommit: "none", ldDate: "unknown",
			info: nil, infoOK: false,
			wantVersion: "dev", wantCommit: "none", wantDateStr: "unknown",
		},
		{
			name:      "ldflags default + BuildInfo with (devel) version — keep default version, take vcs.* fields",
			ldVersion: "dev", ldCommit: "none", ldDate: "unknown",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc123"},
					{Key: "vcs.time", Value: "2026-05-22T12:00:00Z"},
				},
			},
			infoOK:      true,
			wantVersion: "dev", wantCommit: "abc123", wantDateStr: "2026-05-22T12:00:00Z",
		},
		{
			name:      "ldflags default + BuildInfo missing vcs.* settings — keep commit/date defaults",
			ldVersion: "dev", ldCommit: "none", ldDate: "unknown",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.2.0"},
			},
			infoOK:      true,
			wantVersion: "v0.2.0", wantCommit: "none", wantDateStr: "unknown",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, c, d := resolveVersion(tc.ldVersion, tc.ldCommit, tc.ldDate, tc.info, tc.infoOK)
			if v != tc.wantVersion || c != tc.wantCommit || d != tc.wantDateStr {
				t.Errorf("resolveVersion = (%q, %q, %q); want (%q, %q, %q)",
					v, c, d, tc.wantVersion, tc.wantCommit, tc.wantDateStr)
			}
		})
	}
}
