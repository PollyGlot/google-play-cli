// Package coveragedoc renders docs/COVERAGE.md from code (PRD #513, slice
// #514).
//
// COVERAGE.md used to be a hand-maintained table of ~43 *surface* rows standing
// in for 181 Discovery methods, and it drifted: rows claimed ✅ for methods the
// CLI never calls. The fix is to stop writing it. The universe of methods comes
// from the committed existence index (docs/discovery/paths.txt), what the CLI
// calls comes from internal/apiregistry, and what will never be wrapped comes
// from apiregistry.Exclusions. Since ADR-0047 two more declared dispositions
// sit in between: apiregistry.Redundancies (an equivalent shape whose canonical
// is called) and apiregistry.Parkings (a decision deferred to a `type:parking`
// issue). Everything else is uncovered, by subtraction, so no row can lie.
//
// Render is deterministic (same inputs, byte-identical output) so the freshness
// test in this package can re-render and compare bytes, exactly like the Schema
// index gate in internal/schemaindex.
package coveragedoc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/discovery"
)

// State is one of the five answers a method can get (ADR-0026 amended by
// ADR-0047). There is no sixth: a method is called, excluded by nature,
// redundant with a called method, parked behind an issue, or a gap.
type State int

const (
	// Uncovered: in scope under ADR-0026, no command calls it, no disposition.
	Uncovered State = iota
	// Called: at least one shipped command calls it (apiregistry.Entries).
	Called
	// Excluded: never to be wrapped (apiregistry.Exclusions).
	Excluded
	// Redundant: same resource in another shape, served by a called canonical
	// (apiregistry.Redundancies).
	Redundant
	// Parked: decision deferred to a `type:parking` issue
	// (apiregistry.Parkings). Still a gap, but a traced one.
	Parked
)

// issueURL is the tracker a parked row links to.
const issueURL = "https://github.com/PollyGlot/google-play-cli/issues/"

// dispositions is every hand-written answer, indexed by method id, so the
// renderer asks one value per state instead of threading four maps.
type dispositions struct {
	called    map[string]apiregistry.Entry
	excluded  map[string]string
	redundant map[string]apiregistry.Redundant
	parked    map[string]apiregistry.Parked
}

func (d dispositions) state(id string) State {
	switch {
	case d.called[id].MethodID != "":
		return Called
	case d.excluded[id] != "":
		return Excluded
	case d.redundant[id].MethodID != "":
		return Redundant
	case d.parked[id].MethodID != "":
		return Parked
	}
	return Uncovered
}

// Render produces the full contents of docs/COVERAGE.md. paths is the raw
// docs/discovery/paths.txt (`id⇥verb⇥path` per line), the same artefact the
// registry tests anchor to.
func Render(paths []byte) ([]byte, error) {
	ids, err := methodIDs(paths)
	if err != nil {
		return nil, err
	}

	d := dispositions{
		called:    map[string]apiregistry.Entry{},
		excluded:  map[string]string{},
		redundant: map[string]apiregistry.Redundant{},
		parked:    map[string]apiregistry.Parked{},
	}
	for _, e := range apiregistry.Entries() {
		d.called[e.MethodID] = e
	}
	for _, x := range apiregistry.Exclusions() {
		d.excluded[x.MethodID] = x.Reason
	}
	for _, r := range apiregistry.Redundancies() {
		d.redundant[r.MethodID] = r
	}
	for _, p := range apiregistry.Parkings() {
		d.parked[p.MethodID] = p
	}

	// An id in any list that paths.txt does not know would silently vanish
	// from the rendered file, and a method with two dispositions would render
	// as whichever wins the switch: refuse to render instead. (The registry
	// tests catch it too; failing here keeps the generator from committing a
	// quietly incomplete document.)
	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
	}
	for id := range d.called {
		if !known[id] {
			return nil, fmt.Errorf("registry method %q is absent from paths.txt: run `make discovery-update`, or fix the id", id)
		}
	}
	for id := range d.excluded {
		if !known[id] {
			return nil, fmt.Errorf("excluded method %q is absent from paths.txt: run `make discovery-update`, or fix the id", id)
		}
	}
	for id := range d.called {
		if _, dup := d.excluded[id]; dup {
			return nil, fmt.Errorf("method %q is both registered and excluded: a method the CLI calls is not excluded by nature", id)
		}
	}
	if errs := apiregistry.LintDispositions(known); len(errs) > 0 {
		return nil, fmt.Errorf("dispositions: %w", errors.Join(errs...))
	}

	var b bytes.Buffer
	writeHeader(&b)
	writeHeadline(&b, ids, d)
	for _, svc := range discovery.Services {
		writeService(&b, svc, ids, d)
	}
	writeFooter(&b)
	return b.Bytes(), nil
}

// methodIDs parses paths.txt and returns its ids sorted. paths.txt is already
// sorted, but sorting here makes the rendering independent of the producer.
func methodIDs(paths []byte) ([]string, error) {
	var ids []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(paths))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		id, _, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("paths.txt line %q is not `id\\tverb\\tpath`", line)
		}
		if seen[id] {
			return nil, fmt.Errorf("paths.txt lists method %q twice", id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read paths.txt: %w", err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("paths.txt parsed empty: run `make discovery-update`")
	}
	sort.Strings(ids)
	return ids, nil
}

func writeHeader(b *bytes.Buffer) {
	b.WriteString(`# API Coverage Matrix

<!-- Generated by ` + "`make coverage-update`" + `. Do not edit by hand: an offline test
     compares this file against a re-render and fails on any hand-edit. -->

Every method of every API gplay snapshots, in one of five states. Generated
from ` + "`docs/discovery/paths.txt`" + ` (the method universe) and ` + "`internal/apiregistry`" + `
(the methods shipped commands call, plus its exclusion, redundancy and parking
lists), so a row cannot claim coverage the code does not have.

Policy: under [ADR-0026](adr/0026-maximal-admin-api-coverage.md) **every Play
admin API is in scope**; only *runtime* APIs (purchase-token verification, Play
Integrity) are excluded by nature. [ADR-0047](adr/0047-coverage-dispositions-redundant-and-parked.md)
adds two declared dispositions: a method is *redundant* when a called method
already serves the same resource in another shape (the canonical is named on
the row, and an offline test fails if it ever stops being called), and *parked*
when its decision is deferred to a ` + "`type:parking`" + ` issue (linked on the row).
Anything else uncovered is a real gap, not a judgement about whether it is
worth shipping.

| Mark | State | Meaning |
|---|---|---|
| ✅ | called | at least one shipped ` + "`gplay`" + ` command calls it |
| ⚪ | redundant | same resource, another shape; the canonical named on the row is called |
| ⚫️ | excluded | never to be wrapped, with the reason on the row |
| 🔴 | parked | deferred, the row links its ` + "`type:parking`" + ` issue |
| 🔴 | uncovered | in scope, no command calls it, no disposition yet |

`)
}

func writeHeadline(b *bytes.Buffer, ids []string, d dispositions) {
	b.WriteString("## Headline\n\n")
	b.WriteString("| Service | Methods | ✅ called | ⚪ redundant | ⚫️ excluded | 🔴 parked | 🔴 uncovered |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|\n")

	// One counter per State (indexed by its value), per service then summed.
	var tot [Parked + 1]int
	var totMethods int
	for _, svc := range discovery.Services {
		var n int
		var c [Parked + 1]int
		for _, id := range ids {
			if serviceOf(id) != svc.Name {
				continue
			}
			n++
			c[d.state(id)]++
		}
		fmt.Fprintf(b, "| `%s` %s | %d | %d | %d | %d | %d | %d |\n",
			svc.Name, svc.Version, n, c[Called], c[Redundant], c[Excluded], c[Parked], c[Uncovered])
		totMethods += n
		for i := range tot {
			tot[i] += c[i]
		}
	}
	fmt.Fprintf(b, "| **Total** | **%d** | **%d** | **%d** | **%d** | **%d** | **%d** |\n\n",
		totMethods, tot[Called], tot[Redundant], tot[Excluded], tot[Parked], tot[Uncovered])

	if totMethods > 0 {
		fmt.Fprintf(b, "Of the %d admin methods (%d total minus the %d excluded by nature), "+
			"**%d are called**, **%d are redundant** with a called method, "+
			"**%d are parked** behind an issue and **%d are uncovered**.\n\n",
			totMethods-tot[Excluded], totMethods, tot[Excluded], tot[Called], tot[Redundant], tot[Parked], tot[Uncovered])
	}
}

func writeService(b *bytes.Buffer, svc discovery.Service, ids []string, d dispositions) {
	var rows []string
	for _, id := range ids {
		if serviceOf(id) != svc.Name {
			continue
		}
		rows = append(rows, id)
	}
	if len(rows) == 0 {
		return
	}

	fmt.Fprintf(b, "## `%s` %s (%d methods)\n\n", svc.Name, svc.Version, len(rows))
	b.WriteString("| Method | State | Commands / reason |\n|---|---|---|\n")
	for _, id := range rows {
		switch d.state(id) {
		case Called:
			fmt.Fprintf(b, "| `%s` | ✅ | %s |\n", id, calledDetail(d.called[id]))
		case Excluded:
			fmt.Fprintf(b, "| `%s` | ⚫️ | %s |\n", id, escapeCell(d.excluded[id]))
		case Redundant:
			r := d.redundant[id]
			fmt.Fprintf(b, "| `%s` | ⚪ | redundant with `%s`: %s |\n", id, r.CanonicalID, escapeCell(r.Reason))
		case Parked:
			p := d.parked[id]
			fmt.Fprintf(b, "| `%s` | 🔴 | parked, [#%d](%s%d): %s |\n", id, p.Issue, issueURL, p.Issue, escapeCell(p.Reason))
		case Uncovered:
			fmt.Fprintf(b, "| `%s` | 🔴 | |\n", id)
		}
	}
	b.WriteString("\n")
}

// calledDetail renders the commands (and the entry's "why" note, when it has
// one) as one cell.
func calledDetail(e apiregistry.Entry) string {
	cmds := make([]string, 0, len(e.Commands))
	for _, c := range e.Commands {
		cmds = append(cmds, "`gplay "+c+"`")
	}
	detail := strings.Join(cmds, ", ")
	if e.Note != "" {
		// A semicolon, not parentheses: several notes already carry their own
		// parenthesised ADR reference, and nesting them reads badly.
		detail += "; " + escapeCell(e.Note)
	}
	return detail
}

func writeFooter(b *bytes.Buffer) {
	b.WriteString(`## Maintenance

This file is output, not input. To change a row, change its source and
regenerate:

- a command starts (or stops) calling a method: edit ` + "`internal/apiregistry/registry.go`" + `;
- a method is runtime and will never be wrapped: edit ` + "`internal/apiregistry/exclusions.go`" + `,
  with a one-line reason;
- a method is another shape of a resource a called method already serves, or
  its decision is deferred to a ` + "`type:parking`" + ` issue: edit
  ` + "`internal/apiregistry/dispositions.go`" + ` (ADR-0047), naming the called
  canonical or the issue number;
- Google added or removed methods: ` + "`make discovery-update`" + `, then
  ` + "`make schema-index-update`" + `.

Then run ` + "`make coverage-update`" + ` and commit the result. A stale or hand-edited
file fails ` + "`go test ./internal/coveragedoc/`" + ` offline.
`)
}

// serviceOf returns the leading segment of a method id, which Discovery uses as
// the service discriminator.
func serviceOf(id string) string {
	svc, _, _ := strings.Cut(id, ".")
	return svc
}

// escapeCell keeps free prose from breaking the Markdown table: a pipe would
// open a new column, a newline would end the row.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.Join(strings.Fields(s), " ")
}
