package main

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// This file keeps --help honest. TestLeafContract only checks that a leaf HAS
// an Example; the two tests here check what the help SAYS:
//
//   - TestLeafExamples_parse runs every Example invocation through the real
//     flag parser of the leaf it documents, so an Example citing a renamed
//     flag, a wrong value type, a missing required flag or a wrong positional
//     count fails here instead of failing the user who copies it.
//   - TestHelp_noInternalReferences keeps repository-internal pointers (ADR
//     numbers, PRD and issue numbers, repo file paths) out of help text: a
//     Homebrew user cannot open them. Code comments keep them.

// newExampleRoot builds a fresh command tree. Parsing an Example sets flag
// values on the tree it parses into, so each invocation gets its own tree:
// a value left over from a previous Example must never satisfy a required
// flag of the next one.
func newExampleRoot() *cobra.Command {
	return newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
}

// TestLeafExamples_parse checks every "gplay ..." line of every leaf Example:
// it must invoke that leaf, and cobra must accept its flags (name, value
// type, required flags, mutually exclusive groups) and its positional count.
// Deprecated and hidden flags are refused too: an Example is what a new user
// copies, so it only shows the current names.
func TestLeafExamples_parse(t *testing.T) {
	for _, leaf := range runnableLeaves(newExampleRoot()) {
		key := leafKey(leaf)
		invocations := exampleInvocations(leaf.Example)
		if leaf.Example != "" && len(invocations) == 0 {
			t.Errorf("leaf %q Example has no runnable \"gplay ...\" line (only comments or other programs)", key)
		}
		for _, inv := range invocations {
			if err := checkExampleInvocation(key, inv); err != nil {
				t.Errorf("leaf %q Example %q: %v", key, strings.Join(inv, " "), err)
			}
		}
	}
}

// checkExampleInvocation resolves args (everything after "gplay") on a fresh
// tree and runs the same validation cobra runs before RunE.
func checkExampleInvocation(key string, args []string) error {
	cmd := newExampleRoot()
	i := 0
	for ; i < len(args); i++ {
		next := subcommand(cmd, args[i])
		if next == nil {
			break
		}
		cmd = next
	}
	if got := leafKey(cmd); got != key {
		return fmt.Errorf("invokes %q, not the leaf it documents (an Example shows its own leaf only)", got)
	}
	if err := cmd.ParseFlags(args[i:]); err != nil {
		return fmt.Errorf("flag parse: %w", err)
	}
	var stale, missing []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		switch {
		case f.Changed && (f.Deprecated != "" || f.Hidden):
			stale = append(stale, "--"+f.Name)
		case !f.Changed && documentedRequired.MatchString(f.Usage):
			missing = append(missing, "--"+f.Name)
		}
	})
	if len(stale) > 0 {
		return fmt.Errorf("uses deprecated or hidden flag(s) %s", strings.Join(stale, ", "))
	}
	if len(missing) > 0 {
		return fmt.Errorf("omits flag(s) its help documents as required: %s", strings.Join(missing, ", "))
	}
	if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
		return fmt.Errorf("positional args: %w", err)
	}
	if err := cmd.ValidateRequiredFlags(); err != nil {
		return err
	}
	return cmd.ValidateFlagGroups()
}

// documentedRequired matches the usage of a flag its leaf validates as
// required in RunE rather than through cobra's MarkFlagRequired (so that a
// missing value exits 2, not 1): "(required)", "(required; e.g. ...)" or a
// trailing "Required.". Safety gates ("required: this is destructive",
// "required when ...") are not matched: a --dry-run Example omits them.
var documentedRequired = regexp.MustCompile(`\(required(\)|; e\.g\.)|Required\.$`)

func subcommand(c *cobra.Command, name string) *cobra.Command {
	if strings.HasPrefix(name, "-") {
		return nil
	}
	for _, k := range c.Commands() {
		if k.Name() == name || k.HasAlias(name) {
			return k
		}
	}
	return nil
}

// exampleInvocations extracts the gplay invocations of an Example: comment
// and blank lines are skipped, a trailing backslash continues a line, leading
// VAR=value assignments are dropped, and a pipe, redirect or command list
// ends the invocation. Lines running another program (jq, apksigner) are not
// gplay's to check. Each result is the argv after "gplay".
func exampleInvocations(example string) [][]string {
	var out [][]string
	var logical []string
	var cur strings.Builder
	for _, line := range strings.Split(example, "\n") {
		trimmed := strings.TrimSpace(line)
		if cur.Len() == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		if cont, ok := strings.CutSuffix(trimmed, `\`); ok {
			cur.WriteString(cont + " ")
			continue
		}
		cur.WriteString(trimmed)
		logical = append(logical, cur.String())
		cur.Reset()
	}
	if cur.Len() > 0 {
		logical = append(logical, cur.String())
	}
	for _, l := range logical {
		words := shellWords(l)
		for len(words) > 0 && envAssignment.MatchString(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 || words[0] != "gplay" {
			continue
		}
		args := words[1:]
		for i, w := range args {
			if w == "|" || w == "||" || w == "&&" || w == ";" || strings.HasPrefix(w, ">") || strings.HasPrefix(w, "<") || strings.HasPrefix(w, "2>") {
				args = args[:i]
				break
			}
		}
		out = append(out, args)
	}
	return out
}

var envAssignment = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*=`)

// shellWords splits a line the way a POSIX shell would for the subset an
// Example uses: whitespace separates words, single quotes are literal, double
// quotes allow backslash escapes. An operator such as | stays its own word
// only when it is surrounded by spaces, which is how Examples are written.
func shellWords(line string) []string {
	var words []string
	var w strings.Builder
	inWord := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '\'':
			inWord = true
			j := strings.IndexByte(line[i+1:], '\'')
			if j < 0 {
				w.WriteString(line[i+1:])
				i = len(line)
				continue
			}
			w.WriteString(line[i+1 : i+1+j])
			i += j + 1
		case ch == '"':
			inWord = true
			for i++; i < len(line) && line[i] != '"'; i++ {
				if line[i] == '\\' && i+1 < len(line) {
					i++
				}
				w.WriteByte(line[i])
			}
		case ch == '\\' && i+1 < len(line):
			inWord = true
			i++
			w.WriteByte(line[i])
		case ch == ' ' || ch == '\t':
			if inWord {
				words = append(words, w.String())
				w.Reset()
				inWord = false
			}
		default:
			inWord = true
			w.WriteByte(ch)
		}
	}
	if inWord {
		words = append(words, w.String())
	}
	return words
}

// TestExampleParser_selfCheck pins the parser the Example test relies on, so
// a lenient tokenizer cannot turn TestLeafExamples_parse into a silent pass.
func TestExampleParser_selfCheck(t *testing.T) {
	got := exampleInvocations(`  # a comment
  gplay releases upload app.aab --track internal

  GPLAY_READONLY=1 gplay tracks list --output json | jq '.tracks[].track'
  gplay reviews reply --review-id "gp:AOqp TPE" \
    --reply 'Thanks, fixed in 2.1!'
  gplay reviews reply --batch - < replies.tsv
  jq -r . out.json`)
	want := [][]string{
		{"releases", "upload", "app.aab", "--track", "internal"},
		{"tracks", "list", "--output", "json"},
		{"reviews", "reply", "--review-id", "gp:AOqp TPE", "--reply", "Thanks, fixed in 2.1!"},
		{"reviews", "reply", "--batch", "-"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exampleInvocations:\n got %q\nwant %q", got, want)
	}

	// The checker itself must reject what it exists to catch.
	for _, bad := range []struct {
		key  string
		args []string
	}{
		{"releases upload", []string{"releases", "upload", "app.aab", "--trak", "internal"}},
		{"releases upload", []string{"releases", "upload", "app.aab", "--staged", "ten-percent"}},
		{"releases upload", []string{"releases", "list", "--track", "internal"}},
		{"auth logout", []string{"auth", "logout", "--confirm"}},
		{"recovery list", []string{"recovery", "list"}},
		{"install-skills", []string{"install-skills", "--yes"}},
	} {
		if err := checkExampleInvocation(bad.key, bad.args); err == nil {
			t.Errorf("checkExampleInvocation(%q, %q) accepted an invalid invocation", bad.key, bad.args)
		}
	}
}

// internalReference matches pointers into the repository that mean nothing to
// someone who installed the binary: ADR and PRD numbers, issue numbers, and
// repo file paths. The public docs site (https://gplay.sh/docs/...) is where
// help text sends a reader who needs more.
var internalReference = regexp.MustCompile(`ADR-?\d|\bPRD\b|#\d|docs/[A-Za-z_]+\.md|\bCONTEXT\.md\b|\bDESIGN\.md\b|§`)

// TestHelp_noInternalReferences walks every command, groups and root
// included, and checks each piece of text --help prints.
func TestHelp_noInternalReferences(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if name := c.Name(); name == "help" || name == "completion" || strings.HasPrefix(name, "__") {
			return
		}
		fields := map[string]string{"Short": c.Short, "Long": c.Long, "Example": c.Example}
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			fields["flag --"+f.Name] = f.Usage
		})
		for field, text := range fields {
			if m := internalReference.FindString(text); m != "" {
				t.Errorf("%q %s cites %q: help text reaches users who cannot open the repo; state the behaviour, or link a https://gplay.sh/docs page (keep the reference in a code comment)", c.CommandPath(), field, m)
			}
		}
		for _, k := range c.Commands() {
			walk(k)
		}
	}
	walk(newExampleRoot())
}
