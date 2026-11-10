package enumanchor_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// coincidental lists the Discovery enum values that shipped code spells as a
// string literal outside any anchored list, each with why no row is needed.
// Like the ratchet allowlists it can only shrink: an entry the code no longer
// uses fails the test.
var coincidental = map[string]string{
	"CURRENCY":                   "a table column header in prices convert",
	"DAILY":                      "vitals default period, validated against the index by vitals.SupportedPeriods",
	"HOURLY":                     "vitals period, validated against the index by vitals.SupportedPeriods",
	"CRASH":                      "vitals error-type label; unknown types pass through verbatim",
	"NON_FATAL":                  "vitals error-type label; unknown types pass through verbatim",
	"APPLICATION_NOT_RESPONDING": "vitals error-type label; unknown types pass through verbatim",
}

// TestEnumLiteralsAreAnchored closes the gap the table leaves: a list copied
// from Discovery after the table was written. Every string literal in shipped
// Go code that equals a Discovery enum value must be a value of an anchored
// row or an entry of coincidental. Like the table, it sees the scalar and
// parameter enums of the Schema index only; array-item enums such as the team
// permissions have their own anchor in internal/team/vocab.
func TestEnumLiteralsAreAnchored(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	discovery := map[string]bool{}
	add := func(enum []string) {
		for _, v := range enum {
			if !unspecified(v) {
				discovery[v] = true
			}
		}
	}
	for _, s := range idx.Schemas {
		for _, p := range s.Properties {
			add(p.Enum)
		}
	}
	for _, m := range idx.Methods {
		for _, p := range m.Parameters {
			add(p.Enum)
		}
	}
	anchored := map[string]bool{}
	for _, a := range anchors(t) {
		for _, v := range a.code {
			anchored[v] = true
		}
	}

	used := map[string]bool{}
	var offenders []string
	root := filepath.Join("..", "..")
	for _, dir := range []string{"cmd", "commands", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil || !discovery[v] || anchored[v] {
					return true
				}
				if _, ok := coincidental[v]; ok {
					used[v] = true
					return true
				}
				offenders = append(offenders, rel+": "+strconv.Quote(v))
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	slices.Sort(offenders)
	for _, o := range slices.Compact(offenders) {
		t.Errorf("%s is a Discovery enum value outside any anchored list: expose the list and add a row to anchors, or add the value to coincidental with the reason", o)
	}
	for v := range coincidental {
		if !discovery[v] {
			t.Errorf("coincidental lists %q, which Discovery no longer lists: remove the entry", v)
		} else if !used[v] {
			t.Errorf("coincidental lists %q, which no shipped code spells any more: remove the entry", v)
		}
	}
}
