package installskills_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/installskills"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func fixtureRepo(t *testing.T, files map[string]string) (repoDir, commit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	repoDir = t.TempDir()
	for rel, content := range files {
		path := filepath.Join(repoDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		c := exec.CommandContext(t.Context(), "git", args...)
		c.Dir = repoDir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gplay", "GIT_AUTHOR_EMAIL=gplay@example.test",
			"GIT_COMMITTER_NAME=gplay", "GIT_COMMITTER_EMAIL=gplay@example.test",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "--quiet", "--initial-branch", "main")
	git("config", "uploadpack.allowAnySHA1InWant", "true")
	git("add", "-A")
	git("commit", "--quiet", "-m", "fixture pack")

	c := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD")
	c.Dir = repoDir
	out, err := c.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return repoDir, strings.TrimSpace(string(out))
}

var packFiles = map[string]string{
	"README.md":                       "not a skill",
	"skills/gplay-setup/SKILL.md":     "setup skill body\n",
	"skills/gplay-tracks/SKILL.md":    "tracks skill body\n",
	"skills/gplay-tracks/extra.md":    "tracks reference\n",
	"skills/gplay-tracks/nested/a.md": "nested\n",
}

func fixturePin(t *testing.T, skills ...string) (installskills.Pin, string) {
	t.Helper()
	repo, commit := fixtureRepo(t, packFiles)
	if len(skills) == 0 {
		skills = []string{"gplay-setup", "gplay-tracks"}
	}
	sort.Strings(skills)
	return installskills.Pin{
		Repo:   "PollyGlot/google-play-cli-skills",
		URL:    repo,
		Commit: commit,
		Subdir: "skills",
		Skills: skills,
	}, repo
}

func execCmd(t *testing.T, opts installskills.Options, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := installskills.NewCommand(opts)
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(append([]string{}, args...))
	err = cmd.ExecuteContext(context.Background())
	return out.String(), errBuf.String(), err
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestInstallSkills_installsPinnedPack(t *testing.T) {
	pin, _ := fixturePin(t)
	target := t.TempDir()

	mustWrite(t, filepath.Join(target, "my-own-skill", "SKILL.md"), "mine\n")
	mustWrite(t, filepath.Join(target, "gplay-tracks", "SKILL.md"), "stale\n")
	mustWrite(t, filepath.Join(target, "gplay-tracks", "leftover.md"), "stale leftover\n")

	stdout, stderr, err := execCmd(t, installskills.Options{Pin: &pin}, "--dir", target)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}

	if got := read(t, filepath.Join(target, "gplay-setup", "SKILL.md")); got != "setup skill body\n" {
		t.Errorf("gplay-setup/SKILL.md = %q", got)
	}
	if got := read(t, filepath.Join(target, "gplay-tracks", "nested", "a.md")); got != "nested\n" {
		t.Errorf("nested file = %q", got)
	}
	if got := read(t, filepath.Join(target, "gplay-tracks", "SKILL.md")); got != "tracks skill body\n" {
		t.Errorf("stale skill was not replaced: %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, "gplay-tracks", "leftover.md")); !os.IsNotExist(err) {
		t.Error("a file from the previous version of the skill survived the replacement")
	}
	if got := read(t, filepath.Join(target, "my-own-skill", "SKILL.md")); got != "mine\n" {
		t.Errorf("unrelated skill was disturbed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); !os.IsNotExist(err) {
		t.Error("a non-skill repository file was installed")
	}
	for _, e := range dirNames(t, target) {
		if strings.HasPrefix(e, ".gplay-") {
			t.Errorf("temporary directory %q left in the target", e)
		}
	}
	for _, want := range []string{filepath.Join(target, "gplay-setup"), filepath.Join(target, "gplay-tracks")} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing installed path %q\ngot:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "installed 2 skills") {
		t.Errorf("stderr missing the summary\ngot:\n%s", stderr)
	}
}

func TestInstallSkills_wrongCommitLeavesTargetUntouched(t *testing.T) {
	pin, _ := fixturePin(t)
	pin.Commit = "0123456789abcdef0123456789abcdef01234567"
	target := t.TempDir()
	mustWrite(t, filepath.Join(target, "gplay-setup", "SKILL.md"), "previous\n")

	_, _, err := execCmd(t, installskills.Options{Pin: &pin}, "--dir", target)
	if err == nil {
		t.Fatal("expected an error when the pinned commit is absent from the remote")
	}
	if got := exit.For(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if got := read(t, filepath.Join(target, "gplay-setup", "SKILL.md")); got != "previous\n" {
		t.Errorf("previous skills must survive a failed install, got %q", got)
	}
}

func TestInstallSkills_packMismatchInstallsNothing(t *testing.T) {
	pin, _ := fixturePin(t, "gplay-setup", "gplay-tracks", "gplay-vitals")
	target := t.TempDir()
	mustWrite(t, filepath.Join(target, "gplay-setup", "SKILL.md"), "previous\n")

	_, _, err := execCmd(t, installskills.Options{Pin: &pin}, "--dir", target)
	if err == nil {
		t.Fatal("expected an error when the checkout does not match the expected pack")
	}
	if !strings.Contains(err.Error(), "gplay-vitals") {
		t.Errorf("error should name the missing skill, got: %v", err)
	}
	if got := read(t, filepath.Join(target, "gplay-setup", "SKILL.md")); got != "previous\n" {
		t.Errorf("previous skills must survive, got %q", got)
	}
}

func TestInstallSkills_unexpectedSkillInCheckoutIsRefused(t *testing.T) {
	pin, _ := fixturePin(t, "gplay-setup")
	target := t.TempDir()

	_, _, err := execCmd(t, installskills.Options{Pin: &pin}, "--dir", target)
	if err == nil {
		t.Fatal("expected an error when the checkout carries an unlisted skill")
	}
	if !strings.Contains(err.Error(), "gplay-tracks") {
		t.Errorf("error should name the unexpected skill, got: %v", err)
	}
	if names := dirNames(t, target); len(names) != 0 {
		t.Errorf("nothing must be installed, found %v", names)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...)
	c.Dir = dir
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	c.Env = env
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func victimRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "tracked.txt"), "the user's work\n")
	gitIn(t, dir, "init", "--quiet", "--initial-branch", "main")
	gitIn(t, dir, "-c", "user.name=gplay", "-c", "user.email=gplay@example.test", "add", "-A")
	gitIn(t, dir, "-c", "user.name=gplay", "-c", "user.email=gplay@example.test", "commit", "--quiet", "-m", "work")
	return dir
}

func TestInstallSkills_inheritedGitDirDoesNotTouchThatRepo(t *testing.T) {
	pin, _ := fixturePin(t)
	victim := victimRepo(t)
	target := t.TempDir()
	head := gitIn(t, victim, "rev-parse", "HEAD")

	t.Setenv("GIT_DIR", filepath.Join(victim, ".git"))
	t.Setenv("GIT_WORK_TREE", victim)

	_, stderr, err := execCmd(t, installskills.Options{Pin: &pin}, "--dir", target)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	if got := read(t, filepath.Join(target, "gplay-setup", "SKILL.md")); got != "setup skill body\n" {
		t.Errorf("skills were not installed into --dir: %q", got)
	}

	if got := gitIn(t, victim, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Errorf("the user's HEAD was moved: %q", got)
	}
	if got := gitIn(t, victim, "rev-parse", "HEAD"); got != head {
		t.Errorf("the user's HEAD commit changed: %q, want %q", got, head)
	}
	if got := gitIn(t, victim, "remote"); got != "" {
		t.Errorf("a remote was added to the user's repository: %q", got)
	}
	if got := read(t, filepath.Join(victim, "tracked.txt")); got != "the user's work\n" {
		t.Errorf("the user's tracked file was disturbed: %q", got)
	}
	if got := gitIn(t, victim, "status", "--porcelain"); got != "" {
		t.Errorf("the user's working tree is dirty after an install: %q", got)
	}
}

func TestInstallSkills_runsNoConfiguredHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook fixture is a POSIX shell script")
	}
	pin, _ := fixturePin(t)
	target, home := t.TempDir(), t.TempDir()

	hookMarker := filepath.Join(home, "hooks-path-hook-ran")
	templateMarker := filepath.Join(home, "template-hook-ran")
	writeHook(t, filepath.Join(home, "hooks", "post-checkout"), hookMarker)
	writeHook(t, filepath.Join(home, "template", "hooks", "post-checkout"), templateMarker)
	mustWrite(t, filepath.Join(home, "gitconfig"), "[core]\n\thooksPath = "+filepath.Join(home, "hooks")+
		"\n[init]\n\ttemplateDir = "+filepath.Join(home, "template")+"\n")

	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	if _, stderr, err := execCmd(t, installskills.Options{Pin: &pin}, "--dir", target); err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	for _, marker := range []string{hookMarker, templateMarker} {
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("a configured hook was executed (%s)", filepath.Base(marker))
		}
	}
}

func TestInstallSkills_environmentConfigCannotReEnableHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook fixture is a POSIX shell script")
	}
	pin, _ := fixturePin(t)
	target, home := t.TempDir(), t.TempDir()
	marker := filepath.Join(home, "env-hook-ran")
	writeHook(t, filepath.Join(home, "hooks", "post-checkout"), marker)

	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", filepath.Join(home, "hooks"))

	if _, stderr, err := execCmd(t, installskills.Options{Pin: &pin}, "--dir", target); err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a hook configured through the environment was executed")
	}
}

func writeHook(t *testing.T, path, marker string) {
	t.Helper()
	mustWrite(t, path, "#!/bin/sh\necho ran > "+marker+"\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestInstallSkills_adoptsLeftoversOfAnInterruptedRun(t *testing.T) {
	pin, _ := fixturePin(t)
	pin.Commit = "0123456789abcdef0123456789abcdef01234567"
	target := t.TempDir()

	orphanBackup := filepath.Join(target, ".gplay-backup-1234")
	mustWrite(t, filepath.Join(orphanBackup, "gplay-setup", "SKILL.md"), "rescued setup\n")
	mustWrite(t, filepath.Join(orphanBackup, "gplay-tracks", "SKILL.md"), "stale tracks\n")
	mustWrite(t, filepath.Join(target, ".gplay-stage-5678", "gplay-setup", "SKILL.md"), "half staged\n")
	mustWrite(t, filepath.Join(target, "gplay-tracks", "SKILL.md"), "installed tracks\n")

	if _, _, err := execCmd(t, installskills.Options{Pin: &pin}, "--dir", target); err == nil {
		t.Fatal("expected the install itself to fail")
	}

	if got := read(t, filepath.Join(target, "gplay-setup", "SKILL.md")); got != "rescued setup\n" {
		t.Errorf("the skill parked in the leftover backup was not restored: %q", got)
	}
	if got := read(t, filepath.Join(target, "gplay-tracks", "SKILL.md")); got != "installed tracks\n" {
		t.Errorf("an installed skill was overwritten from a leftover backup: %q", got)
	}
	for _, e := range dirNames(t, target) {
		if strings.HasPrefix(e, ".gplay-") {
			t.Errorf("leftover directory %q was not swept", e)
		}
	}
}

func TestInstallSkills_gitAbsent(t *testing.T) {
	pin, _ := fixturePin(t)
	ran := false
	opts := installskills.Options{
		Pin: &pin,
		LookPath: func(string) (string, error) {
			return "", &exec.Error{Name: "git", Err: exec.ErrNotFound}
		},
		Run: func(context.Context, string, []string, string) (string, error) {
			ran = true
			return "", nil
		},
	}

	_, stderr, err := execCmd(t, opts, "--dir", t.TempDir())
	if err == nil {
		t.Fatal("expected an error when git is absent")
	}
	if got := exit.For(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if ran {
		t.Error("git must not be invoked when it is absent")
	}
	for _, want := range []string{"git", "Install git", pin.Commit} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q\ngot:\n%s", want, stderr)
		}
	}
}

func TestInstallSkills_gitLookupError(t *testing.T) {
	pin, _ := fixturePin(t)
	opts := installskills.Options{
		Pin:      &pin,
		LookPath: func(string) (string, error) { return "", errors.New("permission denied") },
	}

	_, stderr, err := execCmd(t, opts, "--dir", t.TempDir())
	if err == nil {
		t.Fatal("expected an error on a lookup failure")
	}
	if strings.Contains(stderr, "Install git") {
		t.Errorf("must not print the 'not found' recipe for a non-ErrNotFound failure\ngot:\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error should surface the real cause, got: %v", err)
	}
}

type exitErr struct{ code int }

func (e exitErr) Error() string { return "exit status 7" }
func (e exitErr) ExitCode() int { return e.code }

func TestInstallSkills_gitFailureIsOpaqueExitOne(t *testing.T) {
	pin, _ := fixturePin(t)
	opts := installskills.Options{
		Pin:      &pin,
		LookPath: func(string) (string, error) { return "/usr/bin/git", nil },
		Run: func(context.Context, string, []string, string) (string, error) {
			return "fatal: could not read from remote repository", exitErr{code: 7}
		},
	}

	_, _, err := execCmd(t, opts, "--dir", t.TempDir())
	if err == nil {
		t.Fatal("expected an error when git fails")
	}
	if got := exit.For(err); got != 1 {
		t.Errorf("exit code = %d, want 1 (git failures are opaque)", got)
	}
	if !strings.Contains(err.Error(), "could not read from remote") {
		t.Errorf("error should carry git's own output, got: %v", err)
	}
}

func TestInstallSkills_rejectsPositionalArgs(t *testing.T) {
	pin, _ := fixturePin(t)
	_, _, err := execCmd(t, installskills.Options{Pin: &pin}, "something")
	if err == nil {
		t.Fatal("expected an error for a positional argument")
	}
}

func TestInstallSkills_legacyFlagsAreDeprecatedNoOps(t *testing.T) {
	pin, _ := fixturePin(t)
	target := t.TempDir()

	stdout, stderr, err := execCmd(t, installskills.Options{Pin: &pin},
		"--dir", target, "--agent", "claude", "--project", "--global", "--yes")
	if err != nil {
		t.Fatalf("legacy flags must be no-ops, got error: %v\nstderr: %s", err, stderr)
	}
	for _, flag := range []string{"--agent", "--project", "--global", "--yes"} {
		want := "warning: " + flag + " is deprecated and ignored"
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q\ngot:\n%s", want, stderr)
		}
	}
	if got := read(t, filepath.Join(target, "gplay-setup", "SKILL.md")); got != "setup skill body\n" {
		t.Errorf("install did not run under legacy flags: %q", got)
	}
	if strings.Contains(stdout, "deprecated") {
		t.Errorf("deprecation warning leaked to stdout:\n%s", stdout)
	}
	help, _, err := execCmd(t, installskills.Options{Pin: &pin}, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if strings.Contains(help, "--agent string") {
		t.Errorf("hidden legacy flag advertised in --help:\n%s", help)
	}
}

func TestInstallSkills_helpDoesNotRunGit(t *testing.T) {
	pin, _ := fixturePin(t)
	ran := false
	opts := installskills.Options{
		Pin:      &pin,
		LookPath: func(string) (string, error) { return "/usr/bin/git", nil },
		Run: func(context.Context, string, []string, string) (string, error) {
			ran = true
			return "", nil
		},
	}

	stdout, _, err := execCmd(t, opts, "--help")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran {
		t.Error("--help must not shell out to git")
	}
	if !strings.Contains(stdout, "git") {
		t.Errorf("help text should state the git requirement\ngot:\n%s", stdout)
	}
}

func TestInstallSkills_metadata(t *testing.T) {
	cmd := installskills.NewCommand(installskills.Options{})
	if cmd.Use != "install-skills" {
		t.Errorf("cmd.Use = %q, want install-skills", cmd.Use)
	}
	for _, want := range []string{"git", "pinned", "rolled back", "verified"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("--help should mention %q\n%s", want, cmd.Long)
		}
	}
}

func TestNoPackageManagerExecution(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body := read(t, name)
		for _, banned := range []string{"npx", "npm", "yarn", "pnpm"} {
			if strings.Contains(body, banned) {
				t.Errorf("%s still references %q", name, banned)
			}
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
