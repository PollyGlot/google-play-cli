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

	if leaves < 13 {
		t.Errorf("walked %d vitals leaves, want >= 13 (did the tree shrink?)", leaves)
	}
}

func TestWithReportingScope_scopesLeavesAddedUnscoped(t *testing.T) {
	leaf := &cobra.Command{Use: "new-leaf"}
	nested := &cobra.Command{Use: "nested-leaf"}
	group := kernel.Group("sub", "", nested)
	vitals := withReportingScope(kernel.Group("vitals", "", leaf, group))

	for _, c := range []*cobra.Command{leaf, nested} {
		if got := kernel.ScopeFor(c); got != token.ReportingScope {
			t.Errorf("leaf %q scope = %q, want the reporting scope", c.CommandPath(), got)
		}
	}
	for _, c := range []*cobra.Command{vitals, group} {
		if got := kernel.ScopeFor(c); got != "" {
			t.Errorf("group %q scope = %q, want none", c.CommandPath(), got)
		}
	}
}

func TestVitalsVersionCodeFlagsComplete(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
	withFlag := 0
	for _, c := range runnableLeaves(root) {
		if !strings.HasPrefix(leafKey(c), "vitals ") || c.Flags().Lookup("version-code") == nil {
			continue
		}
		withFlag++
		if _, ok := c.GetFlagCompletionFunc("version-code"); !ok {
			t.Errorf("%q has --version-code without its completion (vitalscmd.RegisterVersionCodeCompletion)", c.CommandPath())
		}
	}
	if withFlag < 8 {
		t.Errorf("found %d vitals leaves with --version-code, want >= 8", withFlag)
	}
}

func TestVerbVocabulary_canonicalNames(t *testing.T) {
	cases := []struct {
		path     []string
		wantGone bool
	}{
		{[]string{"apps", "view"}, false},
		{[]string{"apps", "info"}, true},
		{[]string{"tracks", "view"}, false},
		{[]string{"tracks", "status"}, true},
		{[]string{"team", "grants", "remove"}, false},
		{[]string{"team", "grants", "revoke"}, true},
		{[]string{"apps", "details", "view"}, false},
		{[]string{"apps", "details", "set"}, false},
		{[]string{"apps", "details"}, false},
		{[]string{"tracks", "availability", "view"}, false},
		{[]string{"tracks", "availability"}, false},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.path, " "), func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			cmd, rest, err := root.Find(tc.path)
			if err != nil {
				t.Fatalf("Find(%v): unexpected error: %v", tc.path, err)
			}
			if tc.wantGone {
				if len(rest) == 0 {
					t.Fatalf("%v: expected the old name to be unresolved (a hard rename leaves no alias), but it resolved to %q", tc.path, cmd.CommandPath())
				}
				return
			}
			if len(rest) != 0 {
				t.Fatalf("%v: expected to resolve fully, but %v was left unconsumed (resolved %q)", tc.path, rest, cmd.CommandPath())
			}
			if want := tc.path[len(tc.path)-1]; cmd.Name() != want {
				t.Fatalf("%v: resolved to leaf %q, want %q", tc.path, cmd.Name(), want)
			}
		})
	}
}

func TestVerbVocabulary_oldLeafNamesFailLoudly(t *testing.T) {
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

var groupPaths = [][]string{
	{},
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
	{"games"},
	{"games", "achievements"},
	{"games", "leaderboards"},
	{"appstore"},
	{"appstore", "catalog"},
	{"appstore", "catalog", "events"},
	{"appstore", "apk"},
	{"appstore", "image"},
	{"appstore", "policy"},
	{"appstore", "publish-status"},
}

func TestGroupCommands_unknownSubcommandFailsLoudly(t *testing.T) {
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
			if out.Len() == 0 {
				t.Errorf("%v: bare group produced no help output", path)
			}
		})
	}
}

func TestUnknownCommand_suggestsNearMatch(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"root-typo", []string{"relaeses"}, "releases"},
		{"root-typo-2", []string{"trakcs"}, "tracks"},
		{"group-typo", []string{"releases", "upalod"}, "upload"},
		{"nested-group-typo", []string{"team", "users", "lst"}, "list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(tc.args)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v: expected a misuse error, got nil", tc.args)
			}
			if code := exit.For(err); code != 2 {
				t.Errorf("%v: exit code = %d, want 2 (CLI misuse); err=%v", tc.args, code, err)
			}
			msg := err.Error()
			if !strings.Contains(msg, "unknown command") {
				t.Errorf("%v: error = %q, want it to name the unknown command", tc.args, msg)
			}
			if !strings.Contains(msg, "Did you mean this?") {
				t.Errorf("%v: error = %q, want a suggestion block", tc.args, msg)
			}
			if !strings.Contains(msg, "\n    "+tc.want) {
				t.Errorf("%v: error = %q, want it to suggest %q on its own indented line", tc.args, msg, tc.want)
			}
			if out.Len() != 0 {
				t.Errorf("%v: stdout = %q, want empty (data channel stays clean)", tc.args, out.String())
			}
		})
	}
}

func TestUnknownCommand_noSuggestionWhenNothingIsClose(t *testing.T) {
	for _, args := range [][]string{
		{"qwertyuiop"},
		{"releases", "qwertyuiop"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(args)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v: expected a misuse error, got nil", args)
			}
			if code := exit.For(err); code != 2 {
				t.Errorf("%v: exit code = %d, want 2 (CLI misuse); err=%v", args, code, err)
			}
			if strings.Contains(err.Error(), "Did you mean") {
				t.Errorf("%v: error = %q, want no suggestion for a nonsense token", args, err)
			}
			if out.Len() != 0 {
				t.Errorf("%v: stdout = %q, want empty", args, out.String())
			}
		})
	}
}

func TestFlagErrors_areCliMisuse(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"unknown-flag-at-root", []string{"--nope"}, "unknown flag"},
		{"unknown-flag-on-leaf", []string{"apps", "list", "--bogus"}, "unknown flag"},
		{"bad-value-at-root", []string{"--verbose=notabool"}, "invalid argument"},
		{"bad-output-value", []string{"apps", "list", "--output", "xyz"}, "unsupported --output"},
		{"missing-required-flag", []string{"apps", "init"}, "missing --package: pass --package"},
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

func TestPositionalArgErrors_areCliMisuse(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"missing-arg-minimum", []string{"orders", "view"}, "missing <orderId>; usage: gplay orders view <orderId>"},
		{"missing-arg-exact", []string{"appstore", "catalog", "view"}, "missing <play-package>; usage: gplay appstore catalog view <play-package>"},
		{"missing-arg-nested-group", []string{"games", "achievements", "view"}, "missing <achievementId>"},
		{"surplus-arg-exact", []string{"tracks", "create", "qa-alpha", "surplus"}, "accepts 1 arg(s), received 2"},
		{"surplus-arg-none-accepted", []string{"apps", "list", "surplus"}, `unexpected argument "surplus": gplay apps list takes no positional arguments`},
		{"surplus-arg-nested-group", []string{"games", "achievements", "list", "surplus"}, `unexpected argument "surplus"`},
		{"surplus-arg-cobra-completion", []string{"completion", "bash", "surplus"}, `unexpected argument "surplus"`},
		{"surplus-arg-version", []string{"version", "surplus"}, `unexpected argument "surplus": gplay version takes no positional arguments`},
		{"surplus-arg-exit-codes", []string{"exit-codes", "surplus"}, `unexpected argument "surplus"`},
		{"surplus-arg-auth-list", []string{"auth", "list", "surplus"}, `unexpected argument "surplus"`},
		{"surplus-arg-auth-status", []string{"auth", "status", "surplus"}, `unexpected argument "surplus"`},
		{"surplus-arg-auth-doctor", []string{"auth", "doctor", "surplus"}, `unexpected argument "surplus"`},
		{"surplus-arg-auth-login", []string{"auth", "login", "surplus"}, `unexpected argument "surplus"`},
		{"surplus-arg-init-names-the-flag", []string{"init", "com.example.app"}, "pass --package com.example.app"},
		{"surplus-arg-apps-init", []string{"apps", "init", "surplus", "--package", "com.example.app"}, `unexpected argument "surplus": gplay apps init takes no positional arguments`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
			root.SetArgs(tc.args)
			var out bytes.Buffer
			root.SetOut(&out)
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
			if out.Len() != 0 {
				t.Errorf("%v: stdout = %q, want empty (data channel stays clean)", tc.args, out.String())
			}
		})
	}
}

func TestArgsValidators_allRouteThroughUsageExit(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	validators, rejections := 0, 0
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Args != nil {
			validators++
			for n := 0; n <= 3; n++ {
				args := make([]string, n)
				for i := range args {
					args[i] = "surplus"
				}
				err := c.Args(c, args)
				if err == nil {
					continue
				}
				rejections++
				if code := exit.For(err); code != 2 {
					t.Errorf("%s: %d positional arg(s) rejected with exit %d, want 2 (CLI misuse); err=%v",
						c.CommandPath(), n, code, err)
				}
			}
		}
		for _, k := range c.Commands() {
			walk(k)
		}
	}
	walk(root)

	if validators < 85 {
		t.Errorf("walked %d Args validators, want >= 85 (did the tree shrink, or did the walk stop early?)", validators)
	}
	if rejections < 85 {
		t.Errorf("only %d bad-arg-count rejections observed, want >= 85 (the assertion above never ran)", rejections)
	}
}

func runnableLeaves(root *cobra.Command) []*cobra.Command {
	var leaves []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if name := c.Name(); name == "help" || name == "completion" || strings.HasPrefix(name, "__") {
			return
		}
		if kids := c.Commands(); len(kids) > 0 {
			for _, k := range kids {
				walk(k)
			}
			return
		}
		leaves = append(leaves, c)
	}
	for _, c := range root.Commands() {
		walk(c)
	}
	return leaves
}

func leafKey(c *cobra.Command) string {
	return strings.TrimPrefix(c.CommandPath(), "gplay ")
}

func TestMutatingRegistry_pinsWriteCommands(t *testing.T) {
	type leaf struct {
		path     []string
		mutating bool
	}
	classified := []leaf{
		{[]string{"init"}, false},
		{[]string{"version"}, false},
		{[]string{"schema"}, false},
		{[]string{"exit-codes"}, false},
		{[]string{"install-skills"}, false},

		{[]string{"auth", "login"}, false},
		{[]string{"auth", "logout"}, false},
		{[]string{"auth", "status"}, false},
		{[]string{"auth", "list"}, false},
		{[]string{"auth", "doctor"}, false},

		{[]string{"apps", "init"}, false},
		{[]string{"apps", "add"}, false},
		{[]string{"apps", "list"}, false},
		{[]string{"apps", "accessible", "list"}, false},
		{[]string{"apps", "audit"}, false},
		{[]string{"apps", "view"}, false},
		{[]string{"apps", "details", "view"}, false},
		{[]string{"apps", "details", "set"}, true},
		{[]string{"apps", "remove"}, false},

		{[]string{"releases", "upload"}, true},
		{[]string{"releases", "promote"}, true},
		{[]string{"releases", "rollout"}, true},
		{[]string{"releases", "halt"}, true},
		{[]string{"releases", "resume"}, true},
		{[]string{"releases", "complete"}, true},
		{[]string{"releases", "list"}, false},
		{[]string{"releases", "mappings", "upload"}, true},
		{[]string{"releases", "sharing", "upload"}, true},
		{[]string{"releases", "expansion-files", "upload"}, true},
		{[]string{"releases", "expansion-files", "set"}, true},
		{[]string{"releases", "expansion-files", "view"}, false},
		{[]string{"releases", "generated", "list"}, false},
		{[]string{"releases", "generated", "download"}, false},
		{[]string{"releases", "system-apks", "create"}, true},
		{[]string{"releases", "system-apks", "list"}, false},
		{[]string{"releases", "system-apks", "view"}, false},
		{[]string{"releases", "system-apks", "download"}, false},
		{[]string{"releases", "artifacts", "list"}, false},

		{[]string{"tracks", "list"}, false},
		{[]string{"tracks", "view"}, false},
		{[]string{"tracks", "create"}, true},
		{[]string{"tracks", "availability", "view"}, false},

		{[]string{"testers", "list"}, false},
		{[]string{"testers", "set"}, true},

		{[]string{"device-tiers", "create"}, true},
		{[]string{"device-tiers", "view"}, false},
		{[]string{"device-tiers", "list"}, false},

		{[]string{"recovery", "create"}, true},
		{[]string{"recovery", "list"}, false},
		{[]string{"recovery", "deploy"}, true},
		{[]string{"recovery", "cancel"}, true},
		{[]string{"recovery", "add-targeting"}, true},

		{[]string{"signing", "enroll"}, true},
		{[]string{"signing", "rotate"}, true},

		{[]string{"team", "permissions"}, false},
		{[]string{"team", "users", "list"}, false},
		{[]string{"team", "users", "view"}, false},
		{[]string{"team", "users", "add"}, true},
		{[]string{"team", "users", "set"}, true},
		{[]string{"team", "users", "remove"}, true},
		{[]string{"team", "grants", "list"}, false},
		{[]string{"team", "grants", "set"}, true},
		{[]string{"team", "grants", "remove"}, true},

		{[]string{"appstore", "create"}, true},

		{[]string{"customapps", "create"}, true},

		{[]string{"appstore", "catalog", "view"}, false},
		{[]string{"appstore", "catalog", "events", "list"}, false},

		{[]string{"appstore", "apk", "upload"}, true},
		{[]string{"appstore", "image", "upload"}, true},
		{[]string{"appstore", "policy", "upload"}, true},
		{[]string{"appstore", "publish-status", "set"}, true},
		{[]string{"appstore", "submit"}, true},

		{[]string{"edits", "begin"}, true},
		{[]string{"edits", "commit"}, true},
		{[]string{"edits", "discard"}, true},
		{[]string{"edits", "status"}, false},
		{[]string{"edits", "validate"}, false},

		{[]string{"games", "achievements", "list"}, false},
		{[]string{"games", "achievements", "view"}, false},
		{[]string{"games", "achievements", "create"}, true},
		{[]string{"games", "achievements", "set"}, true},
		{[]string{"games", "achievements", "remove"}, true},
		{[]string{"games", "leaderboards", "list"}, false},
		{[]string{"games", "leaderboards", "view"}, false},
		{[]string{"games", "leaderboards", "create"}, true},
		{[]string{"games", "leaderboards", "set"}, true},
		{[]string{"games", "leaderboards", "remove"}, true},

		{[]string{"orders", "view"}, false},
		{[]string{"orders", "refund"}, true},
		{[]string{"orders", "voided", "list"}, false},

		{[]string{"subscriptions", "pull"}, false},
		{[]string{"subscriptions", "apply"}, true},
		{[]string{"subscriptions", "prices", "convert"}, false},
		{[]string{"subscriptions", "prices", "migrate"}, true},

		{[]string{"iap", "pull"}, false},
		{[]string{"iap", "apply"}, true},

		{[]string{"reviews", "list"}, false},
		{[]string{"reviews", "reply"}, true},
		{[]string{"reviews", "view"}, false},
		{[]string{"reviews", "history"}, false},

		{[]string{"reports", "list"}, false},
		{[]string{"reports", "installs"}, false},
		{[]string{"reports", "ratings"}, false},
		{[]string{"reports", "crashes"}, false},
		{[]string{"reports", "store-performance"}, false},
		{[]string{"reports", "subscription-stats"}, false},
		{[]string{"reports", "earnings"}, false},
		{[]string{"reports", "sales"}, false},

		{[]string{"vitals", "query"}, false},
		{[]string{"vitals", "crashes"}, false},
		{[]string{"vitals", "anr"}, false},
		{[]string{"vitals", "slowstart"}, false},
		{[]string{"vitals", "slowrendering"}, false},
		{[]string{"vitals", "excessivewakeup"}, false},
		{[]string{"vitals", "lmk"}, false},
		{[]string{"vitals", "stuckbgwakelock"}, false},
		{[]string{"vitals", "errors", "counts"}, false},
		{[]string{"vitals", "errors", "issues"}, false},
		{[]string{"vitals", "errors", "reports"}, false},
		{[]string{"vitals", "anomalies"}, false},
		{[]string{"vitals", "releases"}, false},

		{[]string{"metadata", "list"}, false},
		{[]string{"metadata", "pull"}, false},
		{[]string{"metadata", "validate"}, false},
		{[]string{"metadata", "apply"}, true},
		{[]string{"metadata", "images", "list"}, false},
		{[]string{"metadata", "images", "pull"}, false},
		{[]string{"metadata", "images", "validate"}, false},
		{[]string{"metadata", "images", "apply"}, true},

		{[]string{"compliance", "datasafety", "validate"}, false},
		{[]string{"compliance", "datasafety", "set"}, true},
	}

	want := make(map[string]bool, len(classified))
	for _, e := range classified {
		key := strings.Join(e.path, " ")
		if _, dup := want[key]; dup {
			t.Fatalf("duplicate classification for %q: remove one", key)
		}
		want[key] = e.mutating
	}

	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	seen := map[string]bool{}
	for _, c := range runnableLeaves(root) {
		key := leafKey(c)
		seen[key] = true
		wantMut, ok := want[key]
		if !ok {
			t.Errorf("leaf %q is not classified in the mutating registry: every leaf must be pinned as mutating or read-only (a new write command MUST be kernel.MarkMutating so GPLAY_READONLY refuses it, exit 4)", c.CommandPath())
			continue
		}
		if got := kernel.IsMutating(c); got != wantMut {
			if wantMut {
				t.Errorf("%q must be marked mutating (kernel.MarkMutating): GPLAY_READONLY would not refuse it", key)
			} else {
				t.Errorf("%q is marked mutating but is a read/local command: GPLAY_READONLY would wrongly refuse it", key)
			}
		}
	}

	for key := range want {
		if !seen[key] {
			t.Errorf("classified path %q resolves to no leaf: stale entry (renamed or removed?)", key)
		}
	}
}

func TestStabilityRegistry_pinsPublicContract(t *testing.T) {
	type leaf struct {
		path         []string
		experimental bool
	}
	classified := []leaf{
		{[]string{"init"}, false},
		{[]string{"version"}, false},
		{[]string{"schema"}, true},
		{[]string{"exit-codes"}, false},
		{[]string{"install-skills"}, false},

		{[]string{"auth", "login"}, false},
		{[]string{"auth", "logout"}, false},
		{[]string{"auth", "status"}, false},
		{[]string{"auth", "list"}, false},
		{[]string{"auth", "doctor"}, false},

		{[]string{"apps", "init"}, false},
		{[]string{"apps", "add"}, false},
		{[]string{"apps", "list"}, false},
		{[]string{"apps", "accessible", "list"}, false},
		{[]string{"apps", "audit"}, true},
		{[]string{"apps", "view"}, false},
		{[]string{"apps", "details", "view"}, false},
		{[]string{"apps", "details", "set"}, false},
		{[]string{"apps", "remove"}, false},

		{[]string{"releases", "upload"}, false},
		{[]string{"releases", "promote"}, false},
		{[]string{"releases", "rollout"}, false},
		{[]string{"releases", "halt"}, false},
		{[]string{"releases", "resume"}, false},
		{[]string{"releases", "complete"}, false},
		{[]string{"releases", "list"}, false},
		{[]string{"releases", "mappings", "upload"}, false},
		{[]string{"releases", "sharing", "upload"}, true},
		{[]string{"releases", "expansion-files", "upload"}, true},
		{[]string{"releases", "expansion-files", "set"}, true},
		{[]string{"releases", "expansion-files", "view"}, true},
		{[]string{"releases", "generated", "list"}, true},
		{[]string{"releases", "generated", "download"}, true},
		{[]string{"releases", "system-apks", "create"}, true},
		{[]string{"releases", "system-apks", "list"}, true},
		{[]string{"releases", "system-apks", "view"}, true},
		{[]string{"releases", "system-apks", "download"}, true},
		{[]string{"releases", "artifacts", "list"}, true},

		{[]string{"tracks", "list"}, false},
		{[]string{"tracks", "view"}, false},
		{[]string{"tracks", "create"}, false},
		{[]string{"tracks", "availability", "view"}, false},
		{[]string{"testers", "list"}, false},
		{[]string{"testers", "set"}, false},

		{[]string{"device-tiers", "create"}, true},
		{[]string{"device-tiers", "view"}, true},
		{[]string{"device-tiers", "list"}, true},
		{[]string{"recovery", "create"}, true},
		{[]string{"recovery", "list"}, true},
		{[]string{"recovery", "deploy"}, true},
		{[]string{"recovery", "cancel"}, true},
		{[]string{"recovery", "add-targeting"}, true},
		{[]string{"customapps", "create"}, true},

		{[]string{"signing", "enroll"}, true},
		{[]string{"signing", "rotate"}, true},

		{[]string{"appstore", "create"}, true},
		{[]string{"appstore", "catalog", "view"}, true},
		{[]string{"appstore", "catalog", "events", "list"}, true},
		{[]string{"appstore", "apk", "upload"}, true},
		{[]string{"appstore", "image", "upload"}, true},
		{[]string{"appstore", "policy", "upload"}, true},
		{[]string{"appstore", "publish-status", "set"}, true},
		{[]string{"appstore", "submit"}, true},

		{[]string{"team", "permissions"}, false},
		{[]string{"team", "users", "list"}, false},
		{[]string{"team", "users", "view"}, false},
		{[]string{"team", "users", "add"}, false},
		{[]string{"team", "users", "set"}, false},
		{[]string{"team", "users", "remove"}, false},
		{[]string{"team", "grants", "list"}, false},
		{[]string{"team", "grants", "set"}, false},
		{[]string{"team", "grants", "remove"}, false},
		{[]string{"edits", "begin"}, false},
		{[]string{"edits", "commit"}, false},
		{[]string{"edits", "discard"}, false},
		{[]string{"edits", "status"}, false},
		{[]string{"edits", "validate"}, false},

		{[]string{"games", "achievements", "list"}, true},
		{[]string{"games", "achievements", "view"}, true},
		{[]string{"games", "achievements", "create"}, true},
		{[]string{"games", "achievements", "set"}, true},
		{[]string{"games", "achievements", "remove"}, true},
		{[]string{"games", "leaderboards", "list"}, true},
		{[]string{"games", "leaderboards", "view"}, true},
		{[]string{"games", "leaderboards", "create"}, true},
		{[]string{"games", "leaderboards", "set"}, true},
		{[]string{"games", "leaderboards", "remove"}, true},

		{[]string{"orders", "view"}, true},
		{[]string{"orders", "refund"}, true},
		{[]string{"orders", "voided", "list"}, true},
		{[]string{"subscriptions", "pull"}, true},
		{[]string{"subscriptions", "apply"}, true},
		{[]string{"subscriptions", "prices", "convert"}, true},
		{[]string{"subscriptions", "prices", "migrate"}, true},
		{[]string{"iap", "pull"}, true},
		{[]string{"iap", "apply"}, true},

		{[]string{"reviews", "list"}, false},
		{[]string{"reviews", "reply"}, false},
		{[]string{"reviews", "view"}, false},
		{[]string{"reviews", "history"}, true},

		{[]string{"reports", "list"}, true},
		{[]string{"reports", "installs"}, true},
		{[]string{"reports", "ratings"}, true},
		{[]string{"reports", "crashes"}, true},
		{[]string{"reports", "store-performance"}, true},
		{[]string{"reports", "subscription-stats"}, true},
		{[]string{"reports", "earnings"}, true},
		{[]string{"reports", "sales"}, true},

		{[]string{"vitals", "query"}, false},
		{[]string{"vitals", "crashes"}, false},
		{[]string{"vitals", "anr"}, false},
		{[]string{"vitals", "slowstart"}, false},
		{[]string{"vitals", "slowrendering"}, false},
		{[]string{"vitals", "excessivewakeup"}, false},
		{[]string{"vitals", "lmk"}, false},
		{[]string{"vitals", "stuckbgwakelock"}, false},
		{[]string{"vitals", "errors", "counts"}, false},
		{[]string{"vitals", "errors", "issues"}, false},
		{[]string{"vitals", "errors", "reports"}, false},
		{[]string{"vitals", "anomalies"}, false},
		{[]string{"vitals", "releases"}, true},

		{[]string{"metadata", "list"}, false},
		{[]string{"metadata", "pull"}, false},
		{[]string{"metadata", "validate"}, false},
		{[]string{"metadata", "apply"}, false},
		{[]string{"metadata", "images", "list"}, false},
		{[]string{"metadata", "images", "pull"}, false},
		{[]string{"metadata", "images", "validate"}, false},
		{[]string{"metadata", "images", "apply"}, false},
		{[]string{"compliance", "datasafety", "validate"}, false},
		{[]string{"compliance", "datasafety", "set"}, false},
	}

	want := make(map[string]bool, len(classified))
	for _, e := range classified {
		key := strings.Join(e.path, " ")
		if _, dup := want[key]; dup {
			t.Fatalf("duplicate classification for %q: remove one", key)
		}
		want[key] = e.experimental
	}

	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	seen := map[string]bool{}
	for _, c := range runnableLeaves(root) {
		key := leafKey(c)
		seen[key] = true
		wantExp, ok := want[key]
		if !ok {
			t.Errorf("leaf %q is not classified in the stability registry: every leaf must be pinned as frozen or [experimental]; an unclassified NEW command would silently join the frozen v1.0 Public contract (ADR-0010)", c.CommandPath())
			continue
		}
		if got := kernel.IsExperimental(c); got != wantExp {
			if wantExp {
				t.Errorf("%q must be labelled experimental (kernel.Experimental), as it stands, gplay promises its surface will not change without a major bump", key)
			} else {
				t.Errorf("%q is labelled experimental but is classified as part of the frozen Public contract", key)
			}
		}
		if wantExp && !strings.Contains(c.Short, "[experimental]") {
			t.Errorf("%q is experimental but its Short %q carries no visible marker", key, c.Short)
		}
	}

	for key := range want {
		if !seen[key] {
			t.Errorf("classified path %q resolves to no leaf: stale entry (renamed or removed?)", key)
		}
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
			name:      "ldflags present: keep them, ignore BuildInfo",
			ldVersion: "v9.9.9", ldCommit: "deadbeef", ldDate: "2030-01-01",
			info: buildInfo, infoOK: true,
			wantVersion: "v9.9.9", wantCommit: "deadbeef", wantDateStr: "2030-01-01",
		},
		{
			name:      "ldflags default + BuildInfo: fall back to BuildInfo",
			ldVersion: "dev", ldCommit: "none", ldDate: "unknown",
			info: buildInfo, infoOK: true,
			wantVersion: "v0.1.0-alpha.1", wantCommit: "abc123", wantDateStr: "2026-05-22T12:00:00Z",
		},
		{
			name:      "ldflags default + no BuildInfo: keep defaults",
			ldVersion: "dev", ldCommit: "none", ldDate: "unknown",
			info: nil, infoOK: false,
			wantVersion: "dev", wantCommit: "none", wantDateStr: "unknown",
		},
		{
			name:      "ldflags default + BuildInfo with (devel) version: keep default version, take vcs.* fields",
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
			name:      "ldflags default + BuildInfo missing vcs.* settings: keep commit/date defaults",
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
