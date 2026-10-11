package schema_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	schemacmd "github.com/PollyGlot/google-play-cli/commands/schema"
	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// registryMethods is the expected answer for a command, read straight from the
// API registry: the lens must show exactly what the registry declares, no more
// (a second mapping) and no less (a dropped method).
func registryMethods(command string) []string {
	var ids []string
	for _, e := range apiregistry.Entries() {
		for _, c := range e.Commands {
			if c == command {
				ids = append(ids, e.MethodID)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestCommand_listsTheRegistryMethods asserts --command inverts the registry:
// for a multi-method command and a single-method one, the methods shown are
// exactly the registry's.
func TestCommand_listsTheRegistryMethods(t *testing.T) {
	for _, command := range []string{"releases upload", "edits begin", "metadata apply"} {
		t.Run(command, func(t *testing.T) {
			want := registryMethods(command)
			if len(want) == 0 {
				t.Fatalf("registry declares no method for %q: pick a command it covers", command)
			}
			v := parseJSON(t, renderJSON(t, run(t, schemacmd.Input{Command: command})))
			if got := sortedKeys(v.Methods); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("methods = %v, want the registry's %v", got, want)
			}
		})
	}
	// Spot-check the N:1 case the parking note called "not derivable".
	v := parseJSON(t, renderJSON(t, run(t, schemacmd.Input{Command: "releases upload"})))
	for _, id := range []string{"androidpublisher.edits.bundles.upload", "androidpublisher.edits.tracks.update", "androidpublisher.edits.deobfuscationfiles.upload"} {
		if _, ok := v.Methods[id]; !ok {
			t.Errorf("releases upload must list %s, got %v", id, sortedKeys(v.Methods))
		}
	}
}

// TestCommand_sameViewAsMethodQuery asserts each method renders exactly as a
// method-id query renders it: same method object, same one-hop schemas.
func TestCommand_sameViewAsMethodQuery(t *testing.T) {
	var byCommand, byID struct {
		Methods map[string]json.RawMessage `json:"methods"`
		Schemas map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal([]byte(renderJSON(t, run(t, schemacmd.Input{Command: "edits begin"}))), &byCommand); err != nil {
		t.Fatal(err)
	}
	// "edits begin" calls only edits.insert; the exact id is also a substring
	// of no other method, so the query returns that one method.
	if err := json.Unmarshal([]byte(renderJSON(t, run(t, schemacmd.Input{Query: "androidpublisher.edits.insert"}))), &byID); err != nil {
		t.Fatal(err)
	}
	if len(byCommand.Methods) != 1 || len(byID.Methods) != 1 {
		t.Fatalf("want one method each, got command=%v id=%v", sortedKeys(byCommand.Methods), sortedKeys(byID.Methods))
	}
	id := "androidpublisher.edits.insert"
	if string(byCommand.Methods[id]) != string(byID.Methods[id]) {
		t.Errorf("method view differs:\n--command: %s\nquery:     %s", byCommand.Methods[id], byID.Methods[id])
	}
	for name, raw := range byCommand.Schemas {
		if string(byID.Schemas[name]) != string(raw) {
			t.Errorf("schema %s differs between --command and the id query", name)
		}
	}
	if len(byCommand.Schemas) == 0 {
		t.Error("--command must carry the one-hop request/response schemas")
	}

	table := renderTable(t, run(t, schemacmd.Input{Command: "edits begin"}))
	if want := renderTable(t, run(t, schemacmd.Input{Query: id})); table != want {
		t.Errorf("table view differs:\n--command:\n%s\nquery:\n%s", table, want)
	}
}

// TestCommand_normalizesInput asserts the binary name, extra spaces and case do
// not defeat the lookup.
func TestCommand_normalizesInput(t *testing.T) {
	v := parseJSON(t, renderJSON(t, run(t, schemacmd.Input{Command: "  gplay  Releases   UPLOAD "})))
	if got, want := strings.Join(sortedKeys(v.Methods), ","), strings.Join(registryMethods("releases upload"), ","); got != want {
		t.Errorf("methods = %s, want %s", got, want)
	}
}

// TestCommand_combinesWithMethodAndList asserts --method narrows the command's
// methods by verb and --list renders them as the compact catalog.
func TestCommand_combinesWithMethodAndList(t *testing.T) {
	v := parseJSON(t, renderJSON(t, run(t, schemacmd.Input{Command: "releases upload", Method: "put"})))
	if got := sortedKeys(v.Methods); len(got) != 1 || got[0] != "androidpublisher.edits.tracks.update" {
		t.Errorf("--method PUT methods = %v, want only edits.tracks.update", got)
	}

	r := run(t, schemacmd.Input{Command: "releases upload", List: true})
	table := renderTable(t, r)
	if !strings.Contains(table, "androidpublisher.edits.bundles.upload") || strings.Contains(table, "Parameters") {
		t.Errorf("--list must render the compact catalog of the command's methods:\n%s", table)
	}
	if v := parseJSON(t, renderJSON(t, r)); len(v.Schemas) != 0 {
		t.Errorf("--list JSON carries no schema expansion, got %v", sortedKeys(v.Schemas))
	}
}

// TestCommand_unknownIsANoteAndExit0 asserts a command that calls no API method
// behaves like a zero-match query: empty result, note on stderr, no error.
func TestCommand_unknownIsANoteAndExit0(t *testing.T) {
	tests := []struct {
		name string
		in   schemacmd.Input
		want string
	}{
		{"not a command", schemacmd.Input{Command: "frobnicate now"}, `"frobnicate now" calls no API method`},
		{"offline command", schemacmd.Input{Command: "auth list"}, `"auth list" calls no API method`},
		{"command group", schemacmd.Input{Command: "releases"}, "releases upload"},
		{"verb filter", schemacmd.Input{Command: "edits begin", Method: "DELETE"}, `HTTP verb "DELETE"`},
		{"blank", schemacmd.Input{Command: "   "}, "needs a gplay command path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, stderr := runCaptureStderr(t, tt.in)
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tt.want)
			}
			if v := parseJSON(t, renderJSON(t, r)); len(v.Methods) != 0 || len(v.Schemas) != 0 {
				t.Errorf("want an empty result, got %v / %v", sortedKeys(v.Methods), sortedKeys(v.Schemas))
			}
		})
	}
}

// TestCommand_editLifecycleNote asserts the shared Edit calls the lens does not
// list are named on stderr for an Edit-scoped command, and only there.
func TestCommand_editLifecycleNote(t *testing.T) {
	_, stderr := runCaptureStderr(t, schemacmd.Input{Command: "releases upload"})
	if !strings.Contains(stderr, "edits.insert") || !strings.Contains(stderr, "edits.commit") {
		t.Errorf("releases upload: stderr = %q, want the Edit lifecycle note", stderr)
	}
	if _, stderr := runCaptureStderr(t, schemacmd.Input{Command: "reviews list"}); stderr != "" {
		t.Errorf("reviews list is not Edit-scoped, stderr = %q, want none", stderr)
	}
}

// TestCommand_withQueryIsUsageError asserts a query and --command together are
// CLI misuse (exit 2): they are two keys for the same view, not a filter pair.
func TestCommand_withQueryIsUsageError(t *testing.T) {
	_, err := schemacmd.Run(newOfflineRC(t), schemacmd.Input{Query: "Track", Command: "releases upload"})
	if err == nil {
		t.Fatal("want a usage error")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit code = %d, want 2 (%v)", got, err)
	}
}

// TestCommand_flagRegistered pins the --command flag on the cobra command.
func TestCommand_flagRegistered(t *testing.T) {
	if schemacmd.NewCommand(kernel.Boot{}).Flags().Lookup("command") == nil {
		t.Error("missing --command flag")
	}
}
