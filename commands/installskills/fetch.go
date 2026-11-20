package installskills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const noHooksPath = "/nonexistent/gplay-install-skills-runs-no-hooks"

func hardenedGit(args ...string) []string {
	out := make([]string, 0, len(args)+4)
	out = append(out, "-c", "core.hooksPath="+noHooksPath, "-c", "core.fsmonitor=false")
	return append(out, args...)
}

func fetchPinned(ctx context.Context, run RunFunc, git string, p Pin, dest string) (string, error) {
	steps := [][]string{
		hardenedGit("init", "--quiet", "--template="),
		hardenedGit("remote", "add", "origin", p.URL),
		hardenedGit("fetch", "--quiet", "--depth", "1", "origin", p.Commit),
		hardenedGit("checkout", "--quiet", "--detach", "FETCH_HEAD"),
	}
	for _, args := range steps {
		if out, err := run(ctx, git, args, dest); err != nil {
			return "", fmt.Errorf("git %s: %v%s", subcommandOf(args), err, indentOutput(out)) //nolint:errorlint // wrapping leaks git's exit code (ADR-0045 §5)
		}
	}

	out, err := run(ctx, git, hardenedGit("rev-parse", "HEAD"), dest)
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %v%s", err, indentOutput(out)) //nolint:errorlint // wrapping leaks git's exit code (ADR-0045 §5)
	}
	if got := strings.TrimSpace(out); got != p.Commit {
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

func subcommandOf(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		return args[i]
	}
	return "git"
}

func joinOrNone(v []string) string {
	if len(v) == 0 {
		return "none"
	}
	sort.Strings(v)
	return strings.Join(v, ", ")
}

func indentOutput(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	return "\n    " + strings.ReplaceAll(out, "\n", "\n    ")
}
