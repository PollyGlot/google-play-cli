package list

import "testing"

// TestDefaultColumns_matchRegistryExactly locks the invariant the rest of
// the command relies on: every DefaultColumns key is renderable (present in
// columnRegistry, so renderers never hit a nil value func on the default
// path), and every registry key is in DefaultColumns (so the unknown-column
// error message, which lists DefaultColumns as the valid set, stays
// complete). Drift in either direction fails here at CI time. Mirrors the
// sibling invariant in commands/releases/list.
func TestDefaultColumns_matchRegistryExactly(t *testing.T) {
	for _, k := range DefaultColumns {
		if _, ok := columnRegistry[k]; !ok {
			t.Errorf("DefaultColumns has %q with no columnRegistry entry — default render would emit a blank column", k)
		}
	}
	inDefaults := make(map[string]bool, len(DefaultColumns))
	for _, k := range DefaultColumns {
		inDefaults[k] = true
	}
	for k := range columnRegistry {
		if !inDefaults[k] {
			t.Errorf("columnRegistry has %q absent from DefaultColumns — the unknown-column error would under-report it as valid", k)
		}
	}
}
