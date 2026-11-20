package workflowgate

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const workflowsDir = "../../.github/workflows"

type workflow struct {
	Permissions yaml.Node      `yaml:"permissions"`
	Jobs        map[string]job `yaml:"jobs"`
}

type job struct {
	Uses        string    `yaml:"uses"`
	Permissions yaml.Node `yaml:"permissions"`
}

var levels = map[string]int{"none": 0, "read": 1, "write": 2}

func TestReusableWorkflowCallersGrantCalleePermissions(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(workflowsDir, "*.y*ml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflow found under %s (err %v)", workflowsDir, err)
	}
	calls := 0
	for _, file := range files {
		caller := load(t, file)
		for name, j := range caller.Jobs {
			if !strings.HasPrefix(j.Uses, "./.github/workflows/") {
				continue
			}
			calls++
			granted := effective(t, j.Permissions, caller.Permissions)
			callee := load(t, filepath.Join(workflowsDir, filepath.Base(j.Uses)))
			for calleeJob, cj := range callee.Jobs {
				requested := effective(t, cj.Permissions, callee.Permissions)
				for _, scope := range sortedKeys(requested) {
					if granted[scope] < requested[scope] {
						t.Errorf("%s job %q calls %s, whose job %q requests %s: %s, but the caller grants %s: %s; add it to the caller job's permissions",
							filepath.Base(file), name, j.Uses, calleeJob,
							scope, levelName(requested[scope]), scope, levelName(granted[scope]))
					}
				}
			}
		}
	}
	if calls == 0 {
		t.Fatal("no job calls a local reusable workflow: the release call moved, update this test")
	}
}

func TestEffectivePermissions(t *testing.T) {
	parse := func(s string) yaml.Node {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(s), &n); err != nil {
			t.Fatal(err)
		}
		if len(n.Content) == 0 {
			return yaml.Node{}
		}
		return *n.Content[0]
	}
	got := effective(t, parse("{contents: write, packages: read}"), parse("{contents: read}"))
	if got["contents"] != 2 || got["packages"] != 1 || got["id-token"] != 0 {
		t.Errorf("job map: %v", got)
	}
	got = effective(t, yaml.Node{}, parse("{contents: read}"))
	if got["contents"] != 1 {
		t.Errorf("job inherits the workflow map: %v", got)
	}
	if got = effective(t, parse("write-all"), yaml.Node{}); got["packages"] != 2 {
		t.Errorf("write-all: %v", got)
	}
	if got = effective(t, parse("read-all"), yaml.Node{}); got["packages"] != 1 {
		t.Errorf("read-all: %v", got)
	}
}

var allScopes = []string{
	"actions", "attestations", "checks", "contents", "deployments", "discussions",
	"id-token", "issues", "models", "packages", "pages", "pull-requests",
	"repository-projects", "security-events", "statuses",
}

func effective(t *testing.T, jobPerms, workflowPerms yaml.Node) map[string]int {
	t.Helper()
	n := jobPerms
	if n.Kind == 0 {
		n = workflowPerms
	}
	out := map[string]int{}
	switch n.Kind {
	case 0:
	case yaml.ScalarNode:
		lvl := map[string]int{"read-all": 1, "write-all": 2, "{}": 0}[n.Value]
		for _, s := range allScopes {
			out[s] = lvl
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, ok := levels[n.Content[i+1].Value]
			if !ok {
				t.Fatalf("unknown permission level %q for %s", n.Content[i+1].Value, n.Content[i].Value)
			}
			out[n.Content[i].Value] = v
		}
	default:
		t.Fatalf("unexpected permissions node kind %v", n.Kind)
	}
	return out
}

func load(t *testing.T, path string) workflow {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var w workflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return w
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func levelName(level int) string {
	for k, v := range levels {
		if v == level {
			return k
		}
	}
	return "?"
}
