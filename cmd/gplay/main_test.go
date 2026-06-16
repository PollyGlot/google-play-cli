package main

import (
	"bytes"
	"io"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestVitalsLeavesAreReportingScoped is the end-to-end least-privilege guard for
// PRD #49: EVERY leaf command under `gplay vitals` (query, the seven presets,
// errors counts/issues/reports, anomalies) must request the playdeveloperreporting
// scope via kernel.WithScope. A dropped wrapper anywhere in the registration
// (main.go or errorscmd.NewCommand) makes that leaf silently fall back to the
// androidpublisher scope — this walks the real command tree and catches it.
func TestVitalsLeavesAreReportingScoped(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	var vitals *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "vitals" {
			vitals = c
			break
		}
	}
	if vitals == nil {
		t.Fatal("no `vitals` command registered")
	}

	leaves := 0
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		kids := c.Commands()
		if len(kids) == 0 {
			leaves++
			if got := kernel.ScopeFor(c); got != token.ReportingScope {
				t.Errorf("leaf %q scope = %q, want the reporting scope", c.CommandPath(), got)
			}
			return
		}
		for _, k := range kids {
			walk(k)
		}
	}
	walk(vitals)

	// query + 7 presets + errors{counts,issues,reports} + anomalies = 12.
	if leaves < 12 {
		t.Errorf("walked %d vitals leaves, want >= 12 (did the tree shrink?)", leaves)
	}
}

// TestVerbVocabulary_canonicalNames asserts the ADR-0019 verb renames at
// the user-facing command-tree level: the canonical names resolve to a
// command, and the pre-rename names are gone (a hard rename leaves no
// alias, so the old verb is not a registered subcommand). This is the
// contract guard for the verb audit (#98) — one e2e case per rename,
// exercised through the real cobra tree via Command.Find, which resolves
// commands without executing them or touching auth/network.
//
// Pre-rename verbs appear here only as SPLIT path args, never as a
// contiguous phrase, so the repo-wide verb gate (#168) stays green on this
// file. The subtest name is the path joined at runtime, not source text.
func TestVerbVocabulary_canonicalNames(t *testing.T) {
	cases := []struct {
		path     []string
		wantGone bool // true: the old name must NOT resolve (hard-renamed away)
	}{
		{[]string{"apps", "view"}, false}, // #163 (replaced apps + "info")
		{[]string{"apps", "info"}, true},
		{[]string{"tracks", "view"}, false}, // #164 (replaced tracks + "status")
		{[]string{"tracks", "status"}, true},
		{[]string{"team", "grants", "remove"}, false}, // #165 (replaced grants + "revoke")
		{[]string{"team", "grants", "revoke"}, true},
		{[]string{"apps", "details", "view"}, false},        // #166 (read now carries a verb)
		{[]string{"apps", "details", "set"}, false},         // write unchanged
		{[]string{"apps", "details"}, false},                // group still resolves (bare → help)
		{[]string{"tracks", "availability", "view"}, false}, // #167 (read now carries a verb)
		{[]string{"tracks", "availability"}, false},         // group still resolves (bare → help)
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.path, " "), func(t *testing.T) {
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

// TestVerbVocabulary_oldLeafNamesFailLoudly asserts the runtime half of the
// hard rename: executing a removed leaf verb under its group does not quietly
// print group help with exit 0 (cobra's default for an unknown subcommand of
// a child group). The group rejects it as CLI misuse — exit 2, message naming
// the unknown command — so a CI step still calling the old name breaks.
func TestVerbVocabulary_oldLeafNamesFailLoudly(t *testing.T) {
	// Old names kept as SPLIT args (never a contiguous phrase) for the #168 gate.
	for _, args := range [][]string{
		{"apps", "info"},
		{"tracks", "status"},
		{"team", "grants", "revoke"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v: expected a misuse error, got nil (the old name silently succeeded)", args)
			}
			if code := exit.For(err); code != 2 {
				t.Errorf("%v: exit code = %d, want 2 (CLI misuse); err=%v", args, code, err)
			}
			if !strings.Contains(err.Error(), "unknown command") {
				t.Errorf("%v: error = %q, want it to name the unknown command", args, err)
			}
		})
	}
}

// groupPaths is the full set of grouping nouns in the command tree — pure
// nouns that carry no business logic, whose only job is to hold subcommands
// (and, when bare, print help). The empty path is the root. Every entry must
// behave identically: bare → help (exit 0), unknown subcommand → loud CLI
// misuse (exit 2). Kept as SPLIT path elements (never a contiguous phrase) so
// the #168 verb gate stays green on this file.
var groupPaths = [][]string{
	{}, // root (gplay)
	{"auth"},
	{"apps"},
	{"apps", "details"},
	{"releases"},
	{"tracks"},
	{"tracks", "availability"},
	{"testers"},
	{"team"},
	{"team", "users"},
	{"team", "grants"},
	{"reviews"},
	{"metadata"},
	{"metadata", "images"},
	{"compliance"},
	{"compliance", "datasafety"},
}

// TestGroupCommands_unknownSubcommandFailsLoudly asserts the UX contract for
// EVERY grouping noun (and the root): a mistyped or unknown subcommand is
// rejected as CLI misuse — exit 2, message naming the unknown command — never
// the cobra default of silently printing group help with exit 0. This is the
// generalisation of TestVerbVocabulary_oldLeafNamesFailLoudly (which guards
// the specific ADR-0019 renames) to the whole tree, so a typo against any
// group breaks loudly instead of "succeeding".
func TestGroupCommands_unknownSubcommandFailsLoudly(t *testing.T) {
	// An invented token that is not a subcommand of any group (and not a
	// pre-rename verb, so the #168 gate is untouched).
	const bogus = "nonesuch"
	for _, path := range groupPaths {
		args := append(append([]string{}, path...), bogus)
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v: expected a misuse error, got nil (the group silently printed help)", args)
			}
			if code := exit.For(err); code != 2 {
				t.Errorf("%v: exit code = %d, want 2 (CLI misuse); err=%v", args, code, err)
			}
			if !strings.Contains(err.Error(), "unknown command") {
				t.Errorf("%v: error = %q, want it to name the unknown command", args, err)
			}
		})
	}
}

// TestGroupCommands_bareInvocationPrintsHelp asserts the other half of the
// grouping-noun contract: a bare group (no subcommand) prints its help and
// exits cleanly (nil error → exit 0). Giving every group a RunE for the
// unknown-subcommand case must not regress the bare case into an error.
func TestGroupCommands_bareInvocationPrintsHelp(t *testing.T) {
	for _, path := range groupPaths {
		name := strings.Join(path, " ")
		if name == "" {
			name = "(root)"
		}
		t.Run(name, func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(path)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(io.Discard)
			if err := root.Execute(); err != nil {
				t.Fatalf("%v: bare group should print help and succeed, got err=%v", path, err)
			}
			// Help goes to stdout; a non-empty body confirms we printed help
			// rather than silently doing nothing.
			if out.Len() == 0 {
				t.Errorf("%v: bare group produced no help output", path)
			}
		})
	}
}

// TestFlagErrors_areCliMisuse asserts the docs/DESIGN.md §9 contract for the
// flag-error class: an unknown flag, a bad flag value, or a missing required
// flag is CLI misuse — exit 2, named in one "gplay: ..." line — not the
// generic exit 1 cobra hands back for a plain flag-parse error. The root's
// FlagErrorFunc routes parse errors (unknown flag, bad value) through
// exit.Usagef, and is inherited down the whole cobra tree so a leaf's parse
// error maps the same way; `apps init` validates its required --package
// in-band onto the same exit-2 path. Exercised through the real cobra tree via
// Execute, which surfaces the error main.go would hand to exit.For.
//
// Canonical verbs only, and any multi-token path is kept as SPLIT args (never
// a contiguous phrase), so the repo-wide verb gate (#168) stays green here.
func TestFlagErrors_areCliMisuse(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantMsg string // substring the one-line error must contain
	}{
		{"unknown-flag-at-root", []string{"--nope"}, "unknown flag"},
		{"unknown-flag-on-leaf", []string{"apps", "list", "--bogus"}, "unknown flag"},
		// pflag-level bad value: caught by the root FlagErrorFunc.
		{"bad-value-at-root", []string{"--verbose=notabool"}, "invalid argument"},
		// Downstream-validated bad value (--output is resolved in the kernel,
		// not pflag.Set): typed as a UsageError at its source, so it lands on
		// the same exit-2 path without the FlagErrorFunc seeing it.
		{"bad-output-value", []string{"apps", "list", "--output", "xyz"}, "unsupported --output"},
		{"missing-required-flag", []string{"apps", "init"}, "--package is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(tc.args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v: expected a CLI-misuse error, got nil", tc.args)
			}
			if code := exit.For(err); code != 2 {
				t.Errorf("%v: exit code = %d, want 2 (CLI misuse); err=%v", tc.args, code, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("%v: error = %q, want it to contain %q", tc.args, err, tc.wantMsg)
			}
		})
	}
}

// TestMutatingRegistry_pinsWriteCommands is the completeness guard for the
// GPLAY_READONLY policy (#211 / ADR-0024): it pins exactly which leaf commands
// carry the mutating annotation (kernel.MarkMutating). A new write command that
// forgets MarkMutating, or a read command wrongly marked, fails here — so the
// policy's authority boundary cannot silently rot as the tree grows. Resolved
// through the real cobra tree via Command.Find (no execution, no auth/network).
//
// Multi-token paths are kept as SPLIT args (never a contiguous phrase) so the
// repo-wide verb gate (#168) stays green on this file.
func TestMutatingRegistry_pinsWriteCommands(t *testing.T) {
	mutating := [][]string{
		{"releases", "upload"},
		{"releases", "promote"},
		{"releases", "rollout"},
		{"releases", "halt"},
		{"releases", "resume"},
		{"releases", "complete"},
		{"tracks", "create"},
		{"testers", "set"},
		{"team", "users", "add"},
		{"team", "users", "set"},
		{"team", "users", "remove"},
		{"team", "grants", "set"},
		{"team", "grants", "remove"},
		{"reviews", "reply"},
		{"metadata", "apply"},
		{"metadata", "images", "apply"},
		{"compliance", "datasafety", "set"},
		{"apps", "details", "set"},
	}
	// Reads (and local-only registry/credential ops) must stay UNmarked — the
	// policy only blocks mutations of Google Play state, so dashboards and
	// agents can still observe and plan with a production credential.
	readOnly := [][]string{
		{"releases", "list"},
		{"tracks", "list"},
		{"tracks", "view"},
		{"tracks", "availability", "view"},
		{"testers", "list"},
		{"team", "users", "list"},
		{"team", "grants", "list"},
		{"team", "permissions"},
		{"reviews", "list"},
		{"metadata", "list"},
		{"metadata", "pull"},
		{"metadata", "images", "list"},
		{"apps", "list"},
		{"apps", "view"},
		{"apps", "details", "view"},
		{"auth", "status"},
		{"auth", "login"},
		{"schema"},
	}

	find := func(t *testing.T, path []string) *cobra.Command {
		t.Helper()
		root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
		cmd, rest, err := root.Find(path)
		if err != nil || len(rest) != 0 {
			t.Fatalf("Find(%v) did not resolve fully: rest=%v err=%v", path, rest, err)
		}
		return cmd
	}

	for _, p := range mutating {
		t.Run("mutating/"+strings.Join(p, " "), func(t *testing.T) {
			if !kernel.IsMutating(find(t, p)) {
				t.Errorf("%v must be marked mutating (kernel.MarkMutating) — GPLAY_READONLY would not refuse it", p)
			}
		})
	}
	for _, p := range readOnly {
		t.Run("read/"+strings.Join(p, " "), func(t *testing.T) {
			if kernel.IsMutating(find(t, p)) {
				t.Errorf("%v is marked mutating but is a read/local command — GPLAY_READONLY would wrongly refuse it", p)
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
