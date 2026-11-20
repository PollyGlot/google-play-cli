package vocab_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/discovery"
	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
)

type enumItems struct {
	Items struct {
		Enum           []string `json:"enum"`
		EnumDeprecated []bool   `json:"enumDeprecated"`
	} `json:"items"`
}

func loadPermissionEnums(t *testing.T) map[vocab.Scope]map[string]bool {
	t.Helper()
	var svc *discovery.Service
	for i := range discovery.Services {
		if discovery.Services[i].Name == "androidpublisher" {
			svc = &discovery.Services[i]
		}
	}
	if svc == nil {
		t.Fatal("androidpublisher is not a declared discovery service")
	}
	path := filepath.Join("..", "..", "..", "docs", "discovery", svc.SnapshotFilename())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot %s: %v (run `make discovery-update`)", path, err)
	}
	var doc struct {
		Schemas struct {
			User struct {
				Properties struct {
					DeveloperAccountPermissions enumItems `json:"developerAccountPermissions"`
				} `json:"properties"`
			} `json:"User"`
			Grant struct {
				Properties struct {
					AppLevelPermissions enumItems `json:"appLevelPermissions"`
				} `json:"properties"`
			} `json:"Grant"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse snapshot %s: %v", path, err)
	}

	out := map[vocab.Scope]map[string]bool{}
	for scope, field := range map[vocab.Scope]enumItems{
		vocab.Account: doc.Schemas.User.Properties.DeveloperAccountPermissions,
		vocab.App:     doc.Schemas.Grant.Properties.AppLevelPermissions,
	} {
		enums, flags := field.Items.Enum, field.Items.EnumDeprecated
		if len(enums) == 0 {
			t.Fatalf("snapshot has no %s permission enum: the User/Grant shape moved", scope)
		}
		if len(flags) != len(enums) {
			t.Fatalf("%s permission enum has %d values but %d enumDeprecated flags", scope, len(enums), len(flags))
		}
		out[scope] = make(map[string]bool, len(enums))
		for i, e := range enums {
			out[scope][e] = flags[i]
		}
	}
	return out
}

func TestAliases_anchoredToDiscovery(t *testing.T) {
	known := loadPermissionEnums(t)
	for _, a := range vocab.Aliases() {
		for _, scope := range []vocab.Scope{vocab.Account, vocab.App} {
			e, ok := a.Enum(scope)
			if !ok {
				continue
			}
			googleDeprecated, listed := known[scope][e]
			switch {
			case !listed:
				t.Errorf("alias %q resolves to %s under %s scope, which the Discovery snapshot does not list", a.Name, e, scope)
			case googleDeprecated && !a.Deprecated():
				t.Errorf("alias %q resolves to %s, which Google marks enumDeprecated: mark the alias deprecated (keep it, it is Public contract, ADR-0016 amendment #559)", a.Name, e)
			case !googleDeprecated && a.Deprecated():
				t.Errorf("alias %q is marked deprecated but %s is not enumDeprecated in the snapshot: drop the stale marking", a.Name, e)
			}
		}
	}
}
