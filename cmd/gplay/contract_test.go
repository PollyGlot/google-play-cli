package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

var updateContract = flag.Bool("update-contract", false, "rewrite testdata/surface.golden from the cobra tree (make contract-update)")

const surfaceGoldenPath = "testdata/surface.golden"

const surfaceGoldenHeader = `# gplay command surface: every leaf, flag and exit code of the shipped binary.
# Generated from the cobra tree by "make contract-update"; never edit by hand.
# The first field is the stability. In a PR, a removed or modified line starting
# with "frozen" breaks the Public contract (ADR-0010, ADR-0042): CI requires a
# "!" in the PR title or an ADR reference in the PR body. Pure additions are
# compatible and pass (scripts/contract-gate.sh).
`

func TestSurfaceGolden_isFresh(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
	got := renderSurface(root)

	if *updateContract {
		if err := os.MkdirAll(filepath.Dir(surfaceGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(surfaceGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(surfaceGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (run \"make contract-update\" to generate it)", surfaceGoldenPath, err)
	}
	if got != string(want) {
		t.Errorf("cmd/gplay/%s is stale: run \"make contract-update\" and commit the result. "+
			"A removed or modified \"frozen\" line breaks the Public contract and needs a \"!\" in the "+
			"PR title or an ADR reference in the PR body; pure additions are compatible.\n%s", surfaceGoldenPath, lineDiff(string(want), got))
	}
}

func renderSurface(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString(surfaceGoldenHeader)

	b.WriteString("\n# Exit codes (docs/DESIGN.md section 9)\n")
	for _, d := range exit.Catalog() {
		fmt.Fprintf(&b, "frozen exit %d meaning=%q retry-safe=%q\n", d.Code, d.Meaning, d.RetrySafe)
	}

	b.WriteString("\n# Global flags (inherited by every leaf)\n")
	root.LocalFlags().VisitAll(func(f *pflag.Flag) {
		fmt.Fprintf(&b, "frozen global %s\n", flagSpec(f))
	})

	b.WriteString("\n# Leaves\n")
	for _, c := range runnableLeaves(root) {
		stab := "frozen"
		if kernel.IsExperimental(c) {
			stab = "experimental"
		}
		fmt.Fprintf(&b, "%s leaf %q use=%q%s\n", stab, leafKey(c), c.Use, leafMarkers(c))
		for _, f := range leafFlags(root, c) {
			fstab := "frozen"
			if kernel.IsExperimentalFlag(c, f) {
				fstab = "experimental"
			}
			fmt.Fprintf(&b, "%s flag %q %s\n", fstab, leafKey(c), flagSpec(f))
		}
	}
	return b.String()
}

func leafMarkers(c *cobra.Command) string {
	var m strings.Builder
	if kernel.IsMutating(c) {
		m.WriteString(" mutating")
	}
	if c.Hidden {
		m.WriteString(" hidden")
	}
	if c.Deprecated != "" {
		fmt.Fprintf(&m, " deprecated=%q", c.Deprecated)
	}
	if len(c.Aliases) > 0 {
		fmt.Fprintf(&m, " aliases=%q", strings.Join(c.Aliases, ","))
	}
	return m.String()
}

func leafFlags(root, c *cobra.Command) []*pflag.Flag {
	byName := map[string]*pflag.Flag{}
	collect := func(f *pflag.Flag) {
		if f.Name == "help" || root.PersistentFlags().Lookup(f.Name) != nil {
			return
		}
		byName[f.Name] = f
	}
	c.LocalFlags().VisitAll(collect)
	c.InheritedFlags().VisitAll(collect)

	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	flags := make([]*pflag.Flag, 0, len(names))
	for _, n := range names {
		flags = append(flags, byName[n])
	}
	return flags
}

func flagSpec(f *pflag.Flag) string {
	var s strings.Builder
	s.WriteString("--" + f.Name)
	if f.Shorthand != "" {
		s.WriteString(" -" + f.Shorthand)
	}
	fmt.Fprintf(&s, " type=%s default=%q", f.Value.Type(), f.DefValue)
	if req := f.Annotations[cobra.BashCompOneRequiredFlag]; len(req) > 0 && req[0] == "true" {
		s.WriteString(" required")
	}
	if f.Hidden {
		s.WriteString(" hidden")
	}
	if f.Deprecated != "" {
		fmt.Fprintf(&s, " deprecated=%q", f.Deprecated)
	}
	return s.String()
}

func lineDiff(want, got string) string {
	count := func(s string) map[string]int {
		m := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			m[l]++
		}
		return m
	}
	wantN, gotN := count(want), count(got)
	var b strings.Builder
	for _, l := range strings.Split(want, "\n") {
		if gotN[l] < wantN[l] {
			b.WriteString("- " + l + "\n")
			wantN[l]--
		}
	}
	wantN = count(want)
	for _, l := range strings.Split(got, "\n") {
		if wantN[l] < gotN[l] {
			b.WriteString("+ " + l + "\n")
			gotN[l]--
		}
	}
	return b.String()
}

type ratchet struct {
	name    string
	ceiling int
	entries []string
}

func (r ratchet) enforce(t *testing.T, subjects map[string]bool, fix string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, e := range r.entries {
		if allowed[e] {
			t.Errorf("%s: duplicate entry %q", r.name, e)
		}
		allowed[e] = true
	}
	keys := make([]string, 0, len(subjects))
	for k := range subjects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch passes := subjects[k]; {
		case passes && allowed[k]:
			t.Errorf("%s: %q now satisfies the rule: delete its entry and lower the ceiling (the allowlist only shrinks)", r.name, k)
		case !passes && !allowed[k]:
			t.Errorf("%s", strings.ReplaceAll(fix, "{}", k))
		}
	}
	for _, e := range r.entries {
		if _, ok := subjects[e]; !ok {
			t.Errorf("%s: entry %q matches nothing in the command tree: stale (renamed or removed?), delete it and lower the ceiling", r.name, e)
		}
	}
	switch n := len(r.entries); {
	case n > r.ceiling:
		t.Errorf("%s grew to %d entries (ceiling %d): the allowlist only shrinks, fix the new leaf instead", r.name, n, r.ceiling)
	case n < r.ceiling:
		t.Errorf("%s shrank to %d entries: lower its ceiling from %d to %d so it cannot grow back", r.name, n, r.ceiling, n)
	}
}

func TestLeafContract(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
	leaves := runnableLeaves(root)
	if len(leaves) < 100 {
		t.Fatalf("walked %d leaves, want >= 100 (did the walk stop early?)", len(leaves))
	}

	t.Run("Long", func(t *testing.T) {
		subjects := map[string]bool{}
		for _, c := range leaves {
			subjects[leafKey(c)] = ownLong(c) != ""
		}
		leavesWithoutLong.enforce(t, subjects,
			`leaf "{}" has no Long: set cobra.Command.Long to what it does, what it reads and writes, and its safety defaults (a Short repeated by kernel.Experimental does not count)`)
	})

	t.Run("Example", func(t *testing.T) {
		for _, c := range leaves {
			if !strings.Contains(c.Example, "gplay ") {
				t.Errorf(`leaf %q has no Example: set cobra.Command.Example to 2 or 3 realistic "gplay ..." invocations (one --dry-run or --output json variant when the leaf has one)`, leafKey(c))
			}
		}
	})

	t.Run("OutputJSON", func(t *testing.T) {
		probe := &cobra.Command{}
		var sink string
		output.RegisterFlag(probe, &sink)
		ref := probe.Flags().Lookup("output")

		subjects := map[string]bool{}
		for _, c := range leaves {
			key := leafKey(c)
			f := c.Flags().Lookup("output")
			has := f != nil && f.Value.Type() == ref.Value.Type() && f.Usage == ref.Usage
			if reason, ok := leavesWithoutOutputByDesign[key]; ok {
				if has {
					t.Errorf("leavesWithoutOutputByDesign: %q now registers --output: delete its entry (%s)", key, reason)
				}
				continue
			}
			subjects[key] = has
		}
		for key := range leavesWithoutOutputByDesign {
			if !hasLeaf(leaves, key) {
				t.Errorf("leavesWithoutOutputByDesign: entry %q matches no leaf: stale, delete it", key)
			}
		}
		leavesWithoutOutput.enforce(t, subjects,
			`leaf "{}" renders no --output: register it with output.RegisterFlag and render through output.Render so --output json mirrors the API (ADR-0003); a leaf with no structured result is documented in docs/DESIGN.md section 7 and listed in leavesWithoutOutputByDesign`)
	})

	t.Run("FlagVocabulary", func(t *testing.T) {
		subjects := map[string]bool{}
		root.LocalFlags().VisitAll(func(f *pflag.Flag) {
			subjects["--"+f.Name] = flagVocabulary[f.Name] != ""
		})
		drift := map[string]bool{}
		for _, e := range flagNameDrift.entries {
			drift[e] = true
		}
		for _, c := range leaves {
			for _, f := range leafFlags(root, c) {
				if f.Hidden || f.Deprecated != "" {
					continue
				}
				key := leafKey(c) + " --" + f.Name
				subjects[key] = flagVocabulary[f.Name] != "" && !drift[key]
			}
		}
		flagNameDrift.enforce(t, subjects,
			`flag "{}" is not in the flag vocabulary: reuse the canonical name of its concept from flagVocabulary (cmd/gplay/contract_vocabulary_test.go), or add the new concept to that table in the same PR`)
	})

	t.Run("VerbVocabulary", func(t *testing.T) {
		subjects := map[string]bool{}
		for _, c := range leaves {
			key := leafKey(c)
			_, ref := verbPathExceptions[key]
			subjects[key] = ref || verbVocabulary[c.Name()] != ""
		}
		for key := range verbPathExceptions {
			if !hasLeaf(leaves, key) {
				t.Errorf("verbPathExceptions: entry %q matches no leaf: stale, delete it", key)
			}
		}
		verbDrift.enforce(t, subjects,
			`leaf "{}" ends in a verb outside the vocabulary: use a CRUD or admitted domain verb from verbVocabulary (docs/DESIGN.md section 0, ADR-0019); a new domain verb must pass the admission test there and be added to the table`)
	})
}

func TestAPIRegistry_commandsNameRealLeaves(t *testing.T) {
	byKey := map[string]*cobra.Command{}
	for _, c := range runnableLeaves(newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})) {
		byKey[leafKey(c)] = c
	}
	for _, e := range apiregistry.Entries() {
		for _, credited := range e.Commands {
			words := strings.Fields(credited)
			var leaf *cobra.Command
			n := len(words)
			for ; n > 0 && leaf == nil; n-- {
				leaf = byKey[strings.Join(words[:n], " ")]
			}
			if leaf == nil {
				t.Errorf("apiregistry: %s credits %q, which starts with no leaf of the command tree (renamed?)", e.MethodID, credited)
				continue
			}
			for _, w := range words[n+1:] {
				name, isFlag := strings.CutPrefix(w, "--")
				if !isFlag {
					continue
				}
				name, _, _ = strings.Cut(name, "=")
				if leaf.Flags().Lookup(name) == nil {
					t.Errorf("apiregistry: %s credits %q, but %q has no --%s (renamed?)", e.MethodID, credited, leafKey(leaf), name)
				}
			}
		}
	}
}

func hasLeaf(leaves []*cobra.Command, key string) bool {
	for _, c := range leaves {
		if leafKey(c) == key {
			return true
		}
	}
	return false
}

func ownLong(c *cobra.Command) string {
	long := strings.TrimSpace(c.Long)
	if strings.HasPrefix(long, "[experimental]") {
		if i := strings.Index(long, "\n\n"); i >= 0 {
			long = strings.TrimSpace(long[i+2:])
		} else {
			long = ""
		}
	}
	if long == strings.TrimSpace(strings.TrimPrefix(c.Short, "[experimental]")) {
		return ""
	}
	return long
}
