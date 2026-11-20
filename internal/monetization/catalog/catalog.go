package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

type Entry struct {
	ProductID string
	Raw       json.RawMessage
}

func Read(dir string) (map[string]json.RawMessage, error) {
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read catalog directory %q: %w", dir, err)
	}
	local := map[string]json.RawMessage{}
	for _, de := range dirents {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			return nil, fmt.Errorf("read catalog file %q: %w", de.Name(), err)
		}
		var probe struct {
			ProductID string `json:"productId"`
		}
		if err := json.Unmarshal(b, &probe); err != nil {
			return nil, fmt.Errorf("catalog file %q is not a JSON subscription resource: %w", de.Name(), err)
		}
		id := strings.TrimSuffix(de.Name(), ".json")
		if id == "" {
			return nil, fmt.Errorf("catalog file %q has an empty filename stem: the stem is the product ID and cannot be empty", de.Name())
		}
		if probe.ProductID != "" && probe.ProductID != id {
			return nil, fmt.Errorf("catalog file %q declares productId %q: the filename stem is the reconciliation key and must match", de.Name(), probe.ProductID)
		}
		local[id] = json.RawMessage(b)
	}
	return local, nil
}

func ResolveDefault(rel string) (string, error) {
	root, err := pathguard.Root(".")
	if err != nil {
		return "", err
	}
	if _, err := pathguard.Contain(root, rel); err != nil {
		return "", exit.Usagef("%v Pass --dir to write the catalog there on purpose.", err)
	}
	return rel, nil
}

func Write(dir string, entries []Entry) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create catalog directory %q: %w", dir, err)
	}
	keep := map[string]struct{}{}
	for _, e := range entries {
		if e.ProductID == "" || e.ProductID != filepath.Base(e.ProductID) ||
			e.ProductID == "." || e.ProductID == ".." || strings.ContainsAny(e.ProductID, `/\`) {
			return nil, fmt.Errorf("live subscription has unsafe productId %q: refusing to write it as a catalog filename", e.ProductID)
		}
		var m map[string]any
		if err := json.Unmarshal(e.Raw, &m); err != nil {
			return nil, fmt.Errorf("decode live subscription %q: %w", e.ProductID, err)
		}
		delete(m, "packageName")
		delete(m, "archived")
		var b bytes.Buffer
		if err := output.WriteJSON(&b, m); err != nil {
			return nil, fmt.Errorf("encode subscription %q: %w", e.ProductID, err)
		}
		name := e.ProductID + ".json"
		if err := writeNoFollow(dir, name, b.Bytes()); err != nil {
			return nil, err
		}
		keep[name] = struct{}{}
	}
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read catalog directory %q: %w", dir, err)
	}
	var removed []string
	for _, de := range dirents {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		if _, ok := keep[de.Name()]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(dir, de.Name())); err != nil {
			return nil, fmt.Errorf("remove stale catalog file %q: %w", de.Name(), err)
		}
		removed = append(removed, de.Name())
	}
	sort.Strings(removed)
	return removed, nil
}

func writeNoFollow(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("write catalog file %q: %w", name, err)
	}
	tmpName := tmp.Name()
	cleanup := func(cause error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write catalog file %q: %w", name, cause)
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write catalog file %q: %w", name, err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write catalog file %q: %w", name, err)
	}
	return nil
}
