package installskills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fetchPinned materialises the pinned commit into a fresh directory and returns
// the path to the pack root (the Subdir inside the checkout).
//
// It deliberately does not use `git clone`: clone resolves a *ref* (mutable on
// the remote), then we would check out a commit and hope it was reachable.
// init + fetch <commit> asks the remote for that exact object, and git refuses
// the transfer if the server cannot produce it, so a rewritten branch cannot
// substitute a different tree. The final rev-parse re-reads what actually
// landed rather than trusting the fetch to have honoured the request.
func fetchPinned(ctx context.Context, run RunFunc, git string, p Pin, dest string) (string, error) {
	steps := [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", p.URL},
		// --depth 1: only the pinned commit's tree is needed, never its history.
		{"fetch", "--quiet", "--depth", "1", "origin", p.Commit},
		{"checkout", "--quiet", "--detach", "FETCH_HEAD"},
	}
	// %v, not %w, on every git error below: a real failure is an *exec.ExitError,
	// which promotes ExitCode() from os.ProcessState and so satisfies exit.Coder.
	// Wrapping would let exit.For find it and leak git's own code (128) as
	// gplay's. Breaking the chain keeps the message and maps to the documented
	// generic 1.
	for _, args := range steps {
		if out, err := run(ctx, git, args, dest); err != nil {
			return "", fmt.Errorf("git %s: %v%s", args[0], err, indentOutput(out))
		}
	}

	out, err := run(ctx, git, []string{"rev-parse", "HEAD"}, dest)
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %v%s", err, indentOutput(out))
	}
	if got := strings.TrimSpace(out); got != p.Commit {
		// Belt and braces over the fetch: if the checkout is not byte-for-byte
		// the reviewed commit, nothing is installed.
		return "", fmt.Errorf("checkout is at %s, want the pinned commit %s", got, p.Commit)
	}

	root := filepath.Join(dest, p.Subdir)
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("pinned checkout has no %s/ directory: %w", p.Subdir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("pinned checkout entry %s is not a directory", p.Subdir)
	}
	return root, nil
}

// verifyPack asserts the checkout holds exactly the pinned skill set: every
// expected skill present as a directory, and no unexpected extra skill smuggled
// in alongside them. Checked before a single byte is written to the target.
func verifyPack(root string, p Pin) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read pack directory: %w", err)
	}
	found := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		found[e.Name()] = true
	}
	var missing, extra []string
	for _, want := range p.Skills {
		if !found[want] {
			missing = append(missing, want)
		}
	}
	expected := make(map[string]bool, len(p.Skills))
	for _, s := range p.Skills {
		expected[s] = true
	}
	for name := range found {
		if !expected[name] {
			extra = append(extra, name)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return fmt.Errorf("pinned checkout does not match the expected pack (missing: %s; unexpected: %s)",
			joinOrNone(missing), joinOrNone(extra))
	}
	return nil
}

func joinOrNone(v []string) string {
	if len(v) == 0 {
		return "none"
	}
	sort.Strings(v)
	return strings.Join(v, ", ")
}

// indentOutput renders a child process's output as an indented block, or "" if
// it said nothing, so the wrapped error stays readable on one line in the
// common case.
func indentOutput(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	return "\n    " + strings.ReplaceAll(out, "\n", "\n    ")
}
