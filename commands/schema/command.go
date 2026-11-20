package schema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

func normalizeCommand(s string) string {
	fields := strings.Fields(strings.ToLower(s))
	if len(fields) > 0 && fields[0] == "gplay" {
		fields = fields[1:]
	}
	return strings.Join(fields, " ")
}

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

func matchCommand(idx schemaindex.Index, entries []apiregistry.Entry, in Input) result {
	r := result{
		Methods: map[string]schemaindex.Method{},
		Schemas: map[string]schemaindex.Schema{},
	}
	verb := strings.ToUpper(strings.TrimSpace(in.Method))
	for _, id := range methodsCalledBy(entries, normalizeCommand(in.Command)) {
		m, ok := idx.Methods[id]
		if !ok {
			continue
		}
		if verb != "" && m.HTTPMethod != verb {
			continue
		}
		r.Methods[id] = m
	}
	return r
}

func runCommand(rc *kernel.RunContext, idx schemaindex.Index, in Input) Payload {
	entries := apiregistry.Entries()
	res := matchCommand(idx, entries, in)
	if res.empty() {
		rc.Logf("%s", zeroCommandNote(entries, in))
	} else if editScoped(res) {
		rc.Logf("%s", editLifecycleNote)
	}
	return Payload{Index: idx, Result: res, Compact: in.List}
}

const editLifecycleNote = "gplay schema: Edit-scoped methods: the command also opens an Edit (edits.insert) and commits it (edits.commit) or drops it (edits.delete), unless an explicit Edit is pinned (gplay edits begin); those shared calls are not listed"

func editScoped(r result) bool {
	for id := range r.Methods {
		if strings.HasPrefix(id, "androidpublisher.edits.") {
			return true
		}
	}
	return false
}

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
