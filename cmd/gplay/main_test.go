package main

import (
	"runtime/debug"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

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
			name:        "ldflags present — keep them, ignore BuildInfo",
			ldVersion:   "v9.9.9", ldCommit: "deadbeef", ldDate: "2030-01-01",
			info: buildInfo, infoOK: true,
			wantVersion: "v9.9.9", wantCommit: "deadbeef", wantDateStr: "2030-01-01",
		},
		{
			name:        "ldflags default + BuildInfo — fall back to BuildInfo",
			ldVersion:   "dev", ldCommit: "none", ldDate: "unknown",
			info: buildInfo, infoOK: true,
			wantVersion: "v0.1.0-alpha.1", wantCommit: "abc123", wantDateStr: "2026-05-22T12:00:00Z",
		},
		{
			name:        "ldflags default + no BuildInfo — keep defaults",
			ldVersion:   "dev", ldCommit: "none", ldDate: "unknown",
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
