package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

func runLikeMain(t *testing.T, args ...string) (string, int, error) {
	t.Helper()
	dir := t.TempDir()
	root := newRootCmd(kernel.Boot{
		ConfigPath:   filepath.Join(dir, "config.json"),
		KeystoreRoot: filepath.Join(dir, "accounts"),
	})
	var stdout bytes.Buffer
	kernel.SetArgs(root, args)
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	var failure error
	code := execute(context.Background(), root, io.Discard, func(err error) {
		failure = err
		writeFailureEnvelope(&stdout, stdout.Len() > 0, args, err)
	})
	return stdout.String(), code, failure
}

func envelopeOf(t *testing.T, stdout string) output.ErrorDetail {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var env output.ErrorEnvelope
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a JSON error envelope: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout carries more than one JSON object:\n%s", stdout)
	}
	return env.Error
}

func TestFailureEnvelope_cliMisuseUnderJSON(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")
	cases := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"surplus-positional", []string{"tracks", "list", "extra", "--output", "json"}, `unexpected argument "extra"`},
		{"missing-positional", []string{"releases", "upload", "--output", "json"}, "missing <artifact>"},
		{"missing-positional-equals-form", []string{"orders", "view", "--output=json"}, "missing <orderId>"},
		{"unknown-flag-before-output", []string{"tracks", "list", "--bogus", "--output", "json"}, "unknown flag: --bogus"},
		{"repeated-flag", []string{"tracks", "list", "--package", "a", "--package", "b", "--output", "json"}, "repeated flag"},
		{"unknown-subcommand", []string{"tracks", "nonesuch"}, `unknown command "nonesuch"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, code, err := runLikeMain(t, tc.args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2 (unchanged by the envelope); err=%v", code, err)
			}
			d := envelopeOf(t, stdout)
			if d.Code != string(exit.CodeUsageError) || d.ExitCode != 2 {
				t.Errorf("envelope code/exitCode = %s/%d, want USAGE_ERROR/2", d.Code, d.ExitCode)
			}
			if d.Message != err.Error() {
				t.Errorf("envelope message = %q, want the stderr text %q", d.Message, err.Error())
			}
			if !strings.Contains(d.Message, tc.wantMsg) {
				t.Errorf("envelope message = %q, want it to contain %q", d.Message, tc.wantMsg)
			}
		})
	}
}

func TestFailureEnvelope_writtenOnceOrNotAtAll(t *testing.T) {
	t.Setenv(output.EnvDefaultOutput, "")

	t.Run("kernel-already-emitted", func(t *testing.T) {
		stdout, code, err := runLikeMain(t, "releases", "list", "--package", "com.example.app", "--output", "json")
		if code != 2 {
			t.Fatalf("exit code = %d, want 2; err=%v", code, err)
		}
		if d := envelopeOf(t, stdout); !strings.Contains(d.Message, "missing --track: pass --track <name>") {
			t.Errorf("envelope message = %q, want the named --track fix", d.Message)
		}
	})

	for name, args := range map[string][]string{
		"table-format":   {"tracks", "list", "extra", "--output", "table"},
		"markdown":       {"tracks", "list", "extra", "--output", "markdown"},
		"invalid-output": {"tracks", "list", "--output", "yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			stdout, code, err := runLikeMain(t, args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2; err=%v", code, err)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty (no envelope outside JSON)", stdout)
			}
		})
	}

	t.Run("env-default-table", func(t *testing.T) {
		t.Setenv(output.EnvDefaultOutput, "table")
		if stdout, _, _ := runLikeMain(t, "tracks", "list", "extra"); stdout != "" {
			t.Errorf("stdout = %q, want empty under GPLAY_DEFAULT_OUTPUT=table", stdout)
		}
	})
}

func TestOutputFlagValue(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"tracks", "list", "--output", "json"}, "json"},
		{[]string{"tracks", "list", "--output=markdown"}, "markdown"},
		{[]string{"--output", "json", "tracks", "--output", "table"}, "json"},
		{[]string{"tracks", "list", "--output"}, ""},
		{[]string{"tracks", "list", "--", "--output", "json"}, ""},
		{[]string{"tracks", "list"}, ""},
	}
	for _, tc := range cases {
		if got := outputFlagValue(tc.argv); got != tc.want {
			t.Errorf("outputFlagValue(%q) = %q, want %q", tc.argv, got, tc.want)
		}
	}
}

func TestStdoutTally_countsAndKeepsTheDescriptor(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	tally := &stdoutTally{f: f}

	if _, err := tally.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tally, "de"); err != nil {
		t.Fatal(err)
	}
	if tally.n != 5 {
		t.Errorf("counted %d bytes, want 5", tally.n)
	}
	if tally.Fd() != f.Fd() {
		t.Errorf("Fd = %d, want the wrapped file's %d", tally.Fd(), f.Fd())
	}
	if output.IsTerminalFunc()(tally) {
		t.Error("a temp file reported as a terminal through the tally")
	}
}

func TestLeaves_declarePositionalArgs(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if !c.HasSubCommands() && c.Runnable() && c.Args == nil && c != root && c.Name() != "help" {
			t.Errorf("%s: no Args validator; declare cobra.NoArgs (or the positionals it takes)", c.CommandPath())
		}
		for _, k := range c.Commands() {
			walk(k)
		}
	}
	walk(root)
}
