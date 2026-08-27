package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// failingTransport is the RoundTripper the repeated-flag tests install over
// http.DefaultTransport (what the kernel falls back to in production when no
// client is injected): every request it sees is a contract violation, because a
// repeated flag must be rejected during parsing, long before any RunE body can
// mint a token or call the API.
type failingTransport struct {
	t     *testing.T
	calls int
}

func (f *failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls++
	f.t.Errorf("unexpected HTTP request to %s: a repeated flag must be rejected before any network I/O", req.URL)
	return nil, errors.New("no network in tests")
}

// noNetwork swaps http.DefaultTransport for a failing RoundTripper for the
// duration of the test and returns it so the caller can assert the call count.
func noNetwork(t *testing.T) *failingTransport {
	t.Helper()
	ft := &failingTransport{t: t}
	prev := http.DefaultTransport
	http.DefaultTransport = ft
	t.Cleanup(func() { http.DefaultTransport = prev })
	return ft
}

// TestRepeatedFlags_areCliMisuse asserts the fourth door into the
// docs/DESIGN.md §9 misuse contract (PRD #446 / #450): a single-value flag
// passed twice is a usage error (exit 2) naming the flag AND both values,
// instead of pflag's silent last-wins.
//
// The `--track alpha --track production` case is the whole reason the PRD
// exists: last-wins there ships to production with nothing on stdout or stderr
// to say the first value was dropped.
//
// Genuine repeatable flags (pflag.SliceValue) must keep accepting repetition:
// see TestRepeatableFlags_stillAcceptRepetition.
//
// Every case is rejected during flag parsing, so no RunE runs: the installed
// failing transport proves no HTTP left the process.
func TestRepeatedFlags_areCliMisuse(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantMsgs []string // substrings the one-line error must contain
	}{
		{
			"repeated-track-on-leaf",
			[]string{"releases", "promote", "--package", "com.example.app", "--to", "alpha", "--to", "production"},
			[]string{"--to", `"alpha"`, `"production"`},
		},
		{
			"repeated-persistent-flag-before-subcommand",
			[]string{"--account", "a", "--account", "b", "apps", "list"},
			[]string{"--account", `"a"`, `"b"`},
		},
		{
			"repeated-persistent-flag-after-subcommand",
			[]string{"apps", "list", "--account", "a", "--account", "b"},
			[]string{"--account", `"a"`, `"b"`},
		},
		{
			"repeated-bool-flag",
			[]string{"apps", "list", "--verbose", "--verbose"},
			[]string{"--verbose"},
		},
		{
			// Identical values are still ambiguous input, not a no-op: the
			// rejection is about the argv, not about the resolved value.
			"repeated-with-identical-values",
			[]string{"apps", "list", "--account", "same", "--account", "same"},
			[]string{"--account", `"same"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := noNetwork(t)
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
			for _, want := range tc.wantMsgs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v: error = %q, want it to contain %q", tc.args, err, want)
				}
			}
			if out.Len() != 0 {
				t.Errorf("%v: stdout = %q, want empty (data channel stays clean)", tc.args, out.String())
			}
			if ft.calls != 0 {
				t.Errorf("%v: %d HTTP request(s) issued, want 0", tc.args, ft.calls)
			}
		})
	}
}

// TestRepeatableFlags_stillAcceptRepetition is the other half of the contract:
// a flag declared repeatable (StringArrayVar / StringSliceVar → pflag.SliceValue)
// must survive the walk untouched. It parses the tree's real repeatable
// `--locale` and asserts both values landed.
func TestRepeatableFlags_stillAcceptRepetition(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
	apply, _, err := root.Find([]string{"metadata", "images", "apply"})
	if err != nil {
		t.Fatalf("find the images apply leaf: %v", err)
	}
	if err := apply.Flags().Parse([]string{"--locale", "en-US", "--locale", "fr-FR"}); err != nil {
		t.Fatalf("parsing a repeated repeatable flag: %v, want nil", err)
	}
	got, err := apply.Flags().GetStringArray("locale")
	if err != nil {
		t.Fatalf("GetStringArray(locale): %v", err)
	}
	if len(got) != 2 || got[0] != "en-US" || got[1] != "fr-FR" {
		t.Errorf("--locale = %v, want [en-US fr-FR]", got)
	}
}

// TestRepeatedFlags_everyRegisteredFlagRejects is the completeness guard behind
// the case-by-case test above, and the reason #450 lives in the kernel rather
// than in each command: it WALKS the real command tree and asserts that EVERY
// registered single-value flag, at any depth, refuses a second value.
//
// A leaf added tomorrow with a plain StringVar is covered without touching this
// test, because kernel.RejectRepeatedFlags wraps whatever the tree carries;
// what this pins is that newRootCmd keeps CALLING it after registration.
// Dropping that one call turns this red.
//
// Flags are driven directly through pflag (Set is a pure function of the flag's
// value), so no RunE fires: no credentials, no keyring, no network.
func TestRepeatedFlags_everyRegisteredFlagRejects(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	// A parseable sample per flag Type() in the tree. An unlisted type is
	// REPORTED rather than skipped, so a new flag kind cannot slip past this
	// guard by simply not having a sample here.
	samples := map[string]string{
		"string": "x", "bool": "true", "int": "1", "int64": "1",
		"float64": "0.5", "duration": "1s",
	}
	checked, repeatable := 0, 0
	seen := map[*pflag.Flag]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		visit := func(f *pflag.Flag) {
			if seen[f] {
				return
			}
			seen[f] = true
			if _, isSlice := f.Value.(pflag.SliceValue); isSlice {
				repeatable++
				return
			}
			sample, ok := samples[f.Value.Type()]
			if !ok {
				t.Errorf("%s --%s: no sample for flag type %q; add one so the repeated-flag guard covers it",
					c.CommandPath(), f.Name, f.Value.Type())
				return
			}
			checked++
			if err := f.Value.Set(sample); err != nil {
				t.Errorf("%s --%s: first Set(%q) = %v, want nil", c.CommandPath(), f.Name, sample, err)
				return
			}
			err := f.Value.Set(sample)
			if err == nil {
				t.Errorf("%s --%s: second Set(%q) = nil, want a repeated-flag rejection", c.CommandPath(), f.Name, sample)
				return
			}
			if !strings.Contains(err.Error(), "--"+f.Name) {
				t.Errorf("%s --%s: rejection %q does not name the flag", c.CommandPath(), f.Name, err)
			}
		}
		c.PersistentFlags().VisitAll(visit)
		c.Flags().VisitAll(visit)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	if checked == 0 {
		t.Fatal("walked the tree and checked no single-value flag: the walk is broken")
	}
	if repeatable == 0 {
		t.Error("walked the tree and found no repeatable (SliceValue) flag: the exemption is untested")
	}
	t.Logf("repeated-flag guard: %d single-value flags checked, %d repeatable flags exempt", checked, repeatable)
}
