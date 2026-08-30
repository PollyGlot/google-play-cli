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

// fixtureRepo builds a real, local git repository holding a skills pack and
// returns its path and the commit the pin should point at. Everything stays on
// disk: the command's only network-shaped dependency is `git fetch`, and a
// local path exercises the same code path without leaving the machine.
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
		c := exec.Command("git", args...)
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
	// Fetching an explicit object name (rather than a ref) is what the installer
	// does; a local upload-pack refuses it unless this is set. Public forges
	// allow it by default.
	git("config", "uploadpack.allowAnySHA1InWant", "true")
	git("add", "-A")
	git("commit", "--quiet", "-m", "fixture pack")

	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = repoDir
	out, err := c.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return repoDir, strings.TrimSpace(string(out))
}

// packFiles is the fixture pack: two skills, one of them multi-file, plus a
// repo file outside skills/ that must never be installed.
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

// execCmd drives the command end-to-end through cobra, returning what it wrote
// to stdout / stderr and the run error.
func execCmd(t *testing.T, opts installskills.Options, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := installskills.NewCommand(opts)
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	// Pass a non-nil slice even with no args: cobra falls back to os.Args[1:]
	// when c.args is nil, which would leak `go test` flags into the command.
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

	// Prior state: an unrelated skill that must survive, and a stale copy of a
	// pack skill carrying a file the new version does not have.
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
	// The stale copy is replaced wholesale, not merged over.
	if got := read(t, filepath.Join(target, "gplay-tracks", "SKILL.md")); got != "tracks skill body\n" {
		t.Errorf("stale skill was not replaced: %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, "gplay-tracks", "leftover.md")); !os.IsNotExist(err) {
		t.Error("a file from the previous version of the skill survived the replacement")
	}
	// Unrelated skills are preserved.
	if got := read(t, filepath.Join(target, "my-own-skill", "SKILL.md")); got != "mine\n" {
		t.Errorf("unrelated skill was disturbed: %q", got)
	}
	// Only skills/ is installed, never the rest of the repository.
	if _, err := os.Stat(filepath.Join(target, "README.md")); !os.IsNotExist(err) {
		t.Error("a non-skill repository file was installed")
	}
	// No staging or backup scaffolding is left behind.
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
	// A syntactically valid commit that the fixture does not contain: the fetch
	// must fail rather than fall back to a branch tip.
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
	// The pin expects a skill the pinned checkout does not carry: the pack is
	// incomplete, so nothing at all is installed.
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
	// The mirror case: the checkout carries a skill the reviewed pin does not
	// list. Installing the rest and ignoring the intruder would silently accept
	// a tampered pack, so the whole install is refused.
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

// gitIn runs git in dir with the ambient repository-location variables removed,
// so an assertion about the victim repository below cannot be fooled by the very
// GIT_DIR the test sets.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
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

// victimRepo is an ordinary user repository, the one an inherited GIT_DIR would
// point install-skills at.
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

// TestInstallSkills_inheritedGitDirDoesNotTouchThatRepo pins the worst failure
// this command can have: git's own environment says *which* repository it acts
// on, and c.Dir does not override it. git exports GIT_DIR to every hook it runs,
// so `gplay install-skills` from a hook or a wrapper used to run `git init`,
// `remote add origin` and `checkout --detach` against the user's repository,
// detaching their HEAD and deleting their tracked files.
func TestInstallSkills_inheritedGitDirDoesNotTouchThatRepo(t *testing.T) {
	pin, _ := fixturePin(t)
	victim := victimRepo(t)
	target := t.TempDir()
	head := gitIn(t, victim, "rev-parse", "HEAD")

	// Set *after* the fixtures are built: these would otherwise redirect the
	// fixture's own git commands too.
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

// TestInstallSkills_runsNoConfiguredHook is the executable half of ADR-0045's
// "nothing else is executed": hooks and init templates are configuration, so
// they run code during `init` and `checkout` without the pack containing a
// single script. The user's other configuration (proxies, CA bundles) is kept
// on purpose, so this is neutralised with `-c`, not by discarding the config.
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

// TestInstallSkills_environmentConfigCannotReEnableHooks closes the other door:
// GIT_CONFIG_COUNT is `-c` in environment form, so an inherited pair could put
// core.hooksPath back.
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

// TestInstallSkills_adoptsLeftoversOfAnInterruptedRun covers the run that never
// reached its deferred cleanup (Ctrl-C, a reaped CI job): its scaffolding stays
// in the target, possibly holding a skill that is nowhere else. The next run
// repairs that, and it does so *before* the fetch, so it works even when the
// install that follows fails.
func TestInstallSkills_adoptsLeftoversOfAnInterruptedRun(t *testing.T) {
	pin, _ := fixturePin(t)
	pin.Commit = "0123456789abcdef0123456789abcdef01234567" // absent: this run will fail
	target := t.TempDir()

	orphanBackup := filepath.Join(target, ".gplay-backup-1234")
	mustWrite(t, filepath.Join(orphanBackup, "gplay-setup", "SKILL.md"), "rescued setup\n")
	mustWrite(t, filepath.Join(orphanBackup, "gplay-tracks", "SKILL.md"), "stale tracks\n")
	mustWrite(t, filepath.Join(target, ".gplay-stage-5678", "gplay-setup", "SKILL.md"), "half staged\n")
	// gplay-tracks is installed, so the backup's copy of it is the older one.
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
		// What a real exec.LookPath returns when git is not on PATH.
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
	// A non-"not found" lookup failure (git present but not executable): the
	// "install git" recipe would mislead, so the real cause is surfaced instead.
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

// exitErr mimics *exec.ExitError: it carries the child's exit code via an
// ExitCode() method, the same shape as exit.Coder. A naive %w-wrap of a git
// failure would leak the child's code as gplay's: the regression this pins.
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
	// The npx passthrough is gone: a stray positional argument is misuse, not
	// something to forward to a third-party installer.
	_, _, err := execCmd(t, installskills.Options{Pin: &pin}, "something")
	if err == nil {
		t.Fatal("expected an error for a positional argument")
	}
}

// TestInstallSkills_legacyFlagsAreDeprecatedNoOps proves the frozen Public
// contract survives the installer swap (ADR-0045 §4): every documented flag of
// the old npx recipe is still accepted, changes nothing, warns on stderr, and
// the run exits 0 with stdout carrying only data (ADR-0003).
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
	// The install itself ran normally: the flags were ignored, not honored.
	if got := read(t, filepath.Join(target, "gplay-setup", "SKILL.md")); got != "setup skill body\n" {
		t.Errorf("install did not run under legacy flags: %q", got)
	}
	// stdout stays data-only: installed paths, no warning leaked into it.
	if strings.Contains(stdout, "deprecated") {
		t.Errorf("deprecation warning leaked to stdout:\n%s", stdout)
	}
	// The legacy flags stay out of the advertised interface.
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
	// --help documents the two facts the command's safety rests on: the pin and
	// the single runtime requirement.
	for _, want := range []string{"git", "pinned", "rolled back", "verified"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("--help should mention %q\n%s", want, cmd.Long)
		}
	}
}

// TestNoPackageManagerExecution guards the acceptance criterion directly: the
// command must not shell out to npx/npm, nor run a script from the skills repo.
// A source-level check is the honest way to state "this no longer happens".
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
