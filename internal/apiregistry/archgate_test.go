package apiregistry_test

// The contract half of the expand-contract refactor (#513, slice #520).
//
// resolve.go can only make the registry complete by construction if no call
// site keeps a private way to reach the wire. This gate enforces that: it
// parses every shipped Go file under internal/play/ and commands/ and fails on
// any string literal that looks like a Google API base URL or resource path.
// A method whose path is written by hand is a method that ships without a
// registry line, and docs/COVERAGE.md would then omit a called method.
//
// The gate lives here, next to the registry it protects, rather than in a
// package of its own: it asserts the registry's own invariant, and an
// internal/archgate package would carry a single test file and no production
// code.
//
// Scope. Only code that builds real requests is scanned: `_test.go` files and
// test-support packages (a package name ending in "test", e.g.
// commands/games/gamescmd/gamescmdtest) build fixtures, and a fixture URL is
// data, not a call. Import paths are skipped too, being string literals that
// happen to contain segments like "/games/".

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// scanRoots are the two trees that may build HTTP requests, relative to this
// package's directory (go test runs there).
var scanRoots = []string{
	filepath.Join("..", "play"),
	filepath.Join("..", "..", "commands"),
}

// allowedPathPackages is the allow-list, and it has exactly one member.
//
// internal/play/gcs reads the Cloud Storage JSON API for `gplay reviews
// history`. Cloud Storage is not an androidpublisher-family service and has no
// snapshot under docs/discovery/, so there is no Discovery data for Resolve to
// derive a URL from and nothing for registry_test.go to anchor an entry to.
// That package therefore keeps its own base URL, deliberately (see the package
// comment in registry.go). Adding a second member here means the API it calls
// has a Discovery snapshot and belongs in the registry instead.
var allowedPathPackages = map[string]bool{
	filepath.Join("..", "play", "gcs"): true,
}

// forbiddenSubstrings are the path fragments that only appear in a hand-written
// Google API URL: the host of any googleapis.com service, and the first segment
// of every service the CLI talks to.
var forbiddenSubstrings = []string{
	".googleapis.com/",
	"/androidpublisher/",
	"/v1beta1/",
	"/games/",
	"/playcustomapp/",
	"/upload/",
}

func TestNoLiteralAPIPathsOutsideTheRegistry(t *testing.T) {
	for _, root := range scanRoots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if allowedPathPackages[path] {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			scanFile(t, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
}

// scanFile reports every offending string literal of one file, so a run names
// all of them rather than the first.
func scanFile(t *testing.T, path string) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if strings.HasSuffix(file.Name.Name, "test") {
		return
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return false
			}
			value, err := strconv.Unquote(node.Value)
			if err != nil {
				return false
			}
			for _, bad := range forbiddenSubstrings {
				if strings.Contains(value, bad) {
					pos := fset.Position(node.Pos())
					t.Errorf("%s:%d: literal API path %q (contains %q): resolve it through apiregistry.Resolve instead of writing the URL by hand", pos.Filename, pos.Line, value, bad)
					return false
				}
			}
			return false
		}
		return true
	})
}
