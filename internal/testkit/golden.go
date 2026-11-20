package testkit

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata golden files instead of comparing against them")

func Golden(t testing.TB, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", filepath.FromSlash(name))
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("testkit: create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("testkit: write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("testkit: read golden %s: %v (run the test with -update to create it)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from golden %s (run with -update if the change is intended)\n%s", path, firstDiff(want, got))
	}
}

func firstDiff(want, got []byte) string {
	wl := strings.Split(string(want), "\n")
	gl := strings.Split(string(got), "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g || i >= len(wl) || i >= len(gl) {
			return fmt.Sprintf("line %d:\n  want: %s\n  got:  %s", i+1, quoteLine(wl, i), quoteLine(gl, i))
		}
	}
	return "(no line differs; check trailing bytes)"
}

func quoteLine(lines []string, i int) string {
	if i >= len(lines) {
		return "<end of file>"
	}
	return strconv.Quote(lines[i])
}
