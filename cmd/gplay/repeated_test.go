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
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type failingTransport struct {
	t     *testing.T
	calls int
}

func (f *failingTransport) serve(req *http.Request) (*http.Response, error) {
	f.calls++
	f.t.Errorf("unexpected HTTP request to %s: a repeated flag must be rejected before any network I/O", req.URL)
	return nil, errors.New("no network in tests")
}

func noNetwork(t *testing.T) *failingTransport {
	t.Helper()
	ft := &failingTransport{t: t}
	prev := http.DefaultTransport
	http.DefaultTransport = testkit.RoundTripFunc(ft.serve)
	t.Cleanup(func() { http.DefaultTransport = prev })
	return ft
}

func TestRepeatedFlags_areCliMisuse(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantMsgs []string
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

func TestRepeatedFlags_everyRegisteredFlagRejects(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})

	samples := map[string]string{
		"string": "x", "bool": "true", "int": "1", "int64": "1",
		"float64": "0.5", "duration": "1s",
		"stringSlice": "x", "stringArray": "x", "int64Slice": "1", "intSlice": "1",
		"stringToString": "k=v", "stringToInt": "k=1", "stringToInt64": "k=1",
		"cancel|error":     "error",
		"skip|fail":        "skip",
		"skip|update|fail": "update",
	}
	repeatableTypes := map[string]bool{
		"stringToString": true, "stringToInt": true, "stringToInt64": true,
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
			sample, ok := samples[f.Value.Type()]
			if !ok {
				t.Errorf("%s --%s: no sample for flag type %q; add one so the repeated-flag guard covers it",
					c.CommandPath(), f.Name, f.Value.Type())
				return
			}
			_, isSlice := f.Value.(pflag.SliceValue)
			if isSlice || repeatableTypes[f.Value.Type()] {
				repeatable++
				if err := f.Value.Set(sample); err != nil {
					t.Errorf("%s --%s: first Set(%q) = %v, want nil", c.CommandPath(), f.Name, sample, err)
					return
				}
				if err := f.Value.Set(sample); err != nil {
					t.Errorf("%s --%s: second Set(%q) on a repeatable flag = %v, want nil", c.CommandPath(), f.Name, sample, err)
				}
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
