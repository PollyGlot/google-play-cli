package schema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// The --command lens (#527). The Discovery snapshot cannot say which methods a
// gplay command calls, but the API registry can: every request a shipped
// command sends resolves through internal/apiregistry, whose entries name the
// commands calling each method (#513). This file inverts that table, read-only;
// it stays the only command-to-method mapping, so the lens cannot drift from
// what the binary actually calls.

// normalizeCommand turns user input into a registry command path: whitespace
// collapsed, lower-cased, and a leading binary name dropped, so `gplay
// releases upload` and `releases  upload` both look up "releases upload".
func normalizeCommand(s string) string {
	fields := strings.Fields(strings.ToLower(s))
	if len(fields) > 0 && fields[0] == "gplay" {
		fields = fields[1:]
	}
	return strings.Join(fields, " ")
}

// methodsCalledBy inverts the registry for one command path: the ids of every
// method whose entry lists that command, in registry order.
func methodsCalledBy(entries []apiregistry.Entry, command string) []string {
	var ids []string
	for _, e := range entries {
		for _, c := range e.Commands {
			if c == command {
				ids = append(ids, e.MethodID)
				break
			}
		}
	}
	return ids
}

// commandsUnder lists the registered command paths below a group, so a lookup
// on "releases" can point at "releases upload" rather than only say "nothing".
func commandsUnder(entries []apiregistry.Entry, group string) []string {
	seen := map[string]bool{}
	prefix := group + " "
	for _, e := range entries {
		for _, c := range e.Commands {
			if strings.HasPrefix(c, prefix) {
				seen[c] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// matchCommand is the pure --command lookup: the methods the command calls,
// taken from the index so each renders exactly as a method-id query would,
// narrowed by --method when set. No schema-name projection: the request and
// response schemas come in through the usual one-hop expansion.
func matchCommand(idx schemaindex.Index, entries []apiregistry.Entry, in Input) result {
	r := result{
		Methods: map[string]schemaindex.Method{},
		Schemas: map[string]schemaindex.Schema{},
	}
	verb := strings.ToUpper(strings.TrimSpace(in.Method))
	for _, id := range methodsCalledBy(entries, normalizeCommand(in.Command)) {
		m, ok := idx.Methods[id]
		if !ok {
			// registry_test anchors every entry to the index; a miss here means a
			// stale build, and showing the rest beats failing the lookup.
			continue
		}
		if verb != "" && m.HTTPMethod != verb {
			continue
		}
		r.Methods[id] = m
	}
	return r
}

// runCommand serves `gplay schema --command`. Like a query, an empty result is
// a note on stderr and exit 0, not an error: an offline command (auth list,
// schema itself) legitimately calls no API method.
func runCommand(rc *kernel.RunContext, idx schemaindex.Index, in Input) Payload {
	entries := apiregistry.Entries()
	res := matchCommand(idx, entries, in)
	if res.empty() {
		// Best-effort note; a stderr write failure must not fail the command.
		rc.Logf("%s", zeroCommandNote(entries, in))
	}
	return Payload{Index: idx, Result: res, Compact: in.List}
}

// zeroCommandNote explains an empty --command result, telling apart a verb
// filter that excluded everything, a command group, and a path that calls no
// registered method.
func zeroCommandNote(entries []apiregistry.Entry, in Input) string {
	command := normalizeCommand(in.Command)
	verb := strings.ToUpper(strings.TrimSpace(in.Method))
	if command == "" {
		return `gplay schema: --command needs a gplay command path (e.g. --command "releases upload")`
	}
	if verb != "" && len(methodsCalledBy(entries, command)) > 0 {
		return fmt.Sprintf("gplay schema: %q calls no API method with HTTP verb %q", command, verb)
	}
	if under := commandsUnder(entries, command); len(under) > 0 {
		return fmt.Sprintf("gplay schema: %q is a command group; give a command under it: %s", command, strings.Join(under, ", "))
	}
	return fmt.Sprintf("gplay schema: %q calls no API method (an offline command, or not a gplay command: see `gplay --help`)", command)
}
