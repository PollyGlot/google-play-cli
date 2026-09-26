package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// This file keeps the hand-written docs honest about three facts the binary
// already knows: the exit-code taxonomy, the diagnostic-code vocabulary and
// which commands are [experimental]. Each lives in a marked block inside an
// otherwise hand-written page, rendered here from the same source the binary
// prints (internal/exit, kernel.IsExperimental over the real tree). The site
// once claimed its exit-code table "can never drift" while it lacked exit 70,
// and README listed an experimental set five namespaces short (#594).
//
// A block is delimited by two HTML comments, invisible on GitHub and on the
// site:
//
//	<!-- BEGIN GENERATED <name> (make docs-update) -->
//	...
//	<!-- END GENERATED <name> -->
//
// Whatever precedes "<!--" on the BEGIN line (a blockquote's "> ") is repeated
// on every generated line, so a block can sit inside a callout.

// updateDocs rewrites the generated blocks instead of comparing them.
var updateDocs = flag.Bool("update-docs", false, "rewrite the generated blocks of README.md and the website pages (make docs-update)")

// generatedDoc is one generated block: the file it lives in (relative to this
// package, where go test runs), its name in the markers, and its renderer.
type generatedDoc struct {
	path   string
	block  string
	render func(root *cobra.Command) string
}

var generatedDocs = []generatedDoc{
	{"../../README.md", "experimental-commands", renderExperimentalInline},
	{"../../website/src/content/docs/docs/concepts/stability.md", "experimental-commands", renderExperimentalList},
	{"../../website/src/content/docs/docs/concepts/exit-codes.md", "exit-codes", func(*cobra.Command) string { return renderExitCodeTable() }},
	{"../../website/src/content/docs/docs/concepts/exit-codes.md", "diagnostic-codes", func(*cobra.Command) string { return renderDiagnosticTable() }},
}

// TestGeneratedDocs_areFresh fails when a generated block no longer matches the
// binary: a changed exit code, a new diagnostic code or a command gaining or
// losing [experimental] must ship with its docs in the same PR.
func TestGeneratedDocs_areFresh(t *testing.T) {
	root := newRootCmd(kernel.Boot{ConfigPath: "/tmp/x", KeystoreRoot: "/tmp/x"})
	for _, d := range generatedDocs {
		raw, err := os.ReadFile(d.path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := replaceBlock(string(raw), d.block, d.render(root))
		if err != nil {
			t.Errorf("%s: %v", d.path, err)
			continue
		}
		if got == string(raw) {
			continue
		}
		if *updateDocs {
			if err := os.WriteFile(d.path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		t.Errorf("%s: generated block %q is stale: run \"make docs-update\" and commit the result.\n%s",
			strings.TrimPrefix(d.path, "../../"), d.block, lineDiff(string(raw), got))
	}
}

// replaceBlock swaps the body between the markers of block for body, prefixing
// each line with the BEGIN line's own prefix.
func replaceBlock(doc, block, body string) (string, error) {
	begin := "<!-- BEGIN GENERATED " + block + " (make docs-update) -->"
	end := "<!-- END GENERATED " + block + " -->"
	b := strings.Index(doc, begin)
	if b < 0 {
		return "", fmt.Errorf("missing marker %q", begin)
	}
	lineStart := strings.LastIndex(doc[:b], "\n") + 1
	prefix := doc[lineStart:b]
	bodyStart := b + len(begin) + 1 // past the marker's newline
	e := strings.Index(doc[b:], end)
	if e < 0 {
		return "", fmt.Errorf("missing marker %q after %q", end, begin)
	}
	endLineStart := strings.LastIndex(doc[:b+e], "\n") + 1
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		out.WriteString(strings.TrimRight(prefix+line, " ") + "\n")
	}
	return doc[:bodyStart] + out.String() + doc[endLineStart:], nil
}

// experimentalRoots returns the topmost [experimental] command of every
// experimental leaf, sorted by path: `appstore` rather than its 9 leaves, but
// `apps audit` alone when the rest of `apps` is frozen.
func experimentalRoots(root *cobra.Command) []*cobra.Command {
	seen := map[*cobra.Command]bool{}
	var roots []*cobra.Command
	for _, leaf := range runnableLeaves(root) {
		if !kernel.IsExperimental(leaf) || leaf.Hidden {
			continue
		}
		top := leaf
		for p := top.Parent(); p != nil && kernel.IsExperimental(p); p = p.Parent() {
			top = p
		}
		if !seen[top] {
			seen[top] = true
			roots = append(roots, top)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return leafKey(roots[i]) < leafKey(roots[j]) })
	return roots
}

// frozenTopLevel returns every top-level command that still has a frozen leaf,
// so the "everything else" sentence is derived too instead of hand-kept.
func frozenTopLevel(root *cobra.Command) []string {
	seen := map[string]bool{}
	var names []string
	for _, leaf := range runnableLeaves(root) {
		if kernel.IsExperimental(leaf) || leaf.Hidden {
			continue
		}
		name := strings.SplitN(leafKey(leaf), " ", 2)[0]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func codeList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + n + "`"
	}
	return strings.Join(quoted, ", ")
}

// renderExperimentalInline is README's compact form: two wrapped sentences.
func renderExperimentalInline(root *cobra.Command) string {
	var names []string
	for _, c := range experimentalRoots(root) {
		names = append(names, leafKey(c))
	}
	text := "Experimental today: " + codeList(names) + ". Every other command is frozen, across " +
		codeList(frozenTopLevel(root)) + "."
	return wrap(text, 78)
}

// renderExperimentalList is the stability page's form: one bullet per
// experimental root with its Short, then the frozen remainder.
func renderExperimentalList(root *cobra.Command) string {
	var b strings.Builder
	for _, c := range experimentalRoots(root) {
		short := strings.TrimSpace(strings.TrimPrefix(c.Short, "[experimental]"))
		fmt.Fprintf(&b, "- `gplay %s`: %s\n", leafKey(c), short)
	}
	b.WriteString("\n")
	b.WriteString(wrap("Every other command is frozen, across "+codeList(frozenTopLevel(root))+".", 78))
	return b.String()
}

// renderExitCodeTable renders exit.Catalog, the table `gplay exit-codes` prints.
func renderExitCodeTable() string {
	var b strings.Builder
	b.WriteString("| Code | Meaning | Retry-safe? |\n| --- | --- | --- |\n")
	for _, d := range exit.Catalog() {
		fmt.Fprintf(&b, "| `%d` | %s | %s |\n", d.Code, mdCell(d.Meaning), verdict(d.RetrySafe))
	}
	return b.String()
}

// renderDiagnosticTable renders exit.CodeCatalog, the vocabulary of the JSON
// error envelope's `code` field (`gplay schema --codes`).
func renderDiagnosticTable() string {
	var b strings.Builder
	b.WriteString("| Code | Exit | Retryable | Meaning |\n| --- | --- | --- | --- |\n")
	for _, d := range exit.CodeCatalog() {
		fmt.Fprintf(&b, "| `%s` | `%d` | %s | %s |\n", d.Code, d.ExitCode, verdict(exit.RetryableLabel(d.Retryable)), mdCell(d.Meaning))
	}
	return b.String()
}

// verdict capitalises a retry verdict and bolds a plain "yes": the rows a
// retry loop acts on should stand out when scanning the column.
func verdict(s string) string {
	if s == "yes" {
		return "**Yes**"
	}
	if s == "" || s == "n/a" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// mdCell makes catalog prose safe inside a Markdown table cell.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "<", "&lt;")
}

// wrap hard-wraps prose at width, matching the hand-wrapped Markdown around
// the block so a diff of the file stays line-oriented. A code span is never
// split: "`releases sharing`," wraps as one word.
func wrap(text string, width int) string {
	var words []string
	for _, f := range strings.Fields(text) {
		if n := len(words); n > 0 && strings.Count(words[n-1], "`")%2 == 1 {
			words[n-1] += " " + f
			continue
		}
		words = append(words, f)
	}
	var b strings.Builder
	line := 0
	for _, w := range words {
		if line > 0 && line+1+len(w) > width {
			b.WriteString("\n")
			line = 0
		}
		if line > 0 {
			b.WriteString(" ")
			line++
		}
		b.WriteString(w)
		line += len(w)
	}
	return b.String() + "\n"
}
