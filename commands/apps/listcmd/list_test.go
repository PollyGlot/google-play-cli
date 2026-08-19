// Package listcmd_test exercises `gplay apps list` at two levels: the
// pure business function (Run with a hand-built RunContext) and the
// cobra wrapper (NewCommand wired through kernel.RunCobra). Mirrors
// commands/auth/list and commands/apps/addcmd.
package listcmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/apps/listcmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// newRC seeds a RunContext with one Account named "playci" (active) and
// the given packages registered under it, with rc.AccountName populated
// to mirror the post-resolver state (the production resolver populates
// AccountName from the credential that actually resolved: list reads
// it as the source of truth for which Account to scope to). Pin defaults
// to empty unless the test overrides rc.Resolved.Pin after the call.
func newRC(t *testing.T, stdout, stderr *bytes.Buffer, packages []string) *kernel.RunContext {
	t.Helper()
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: stdout,
		Stderr: stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.AccountName = "playci"
	rc.Resolved = &config.Resolved{
		Accounts:      []config.Account{{Name: "playci", Active: true, Packages: packages}},
		ConfigAccount: "playci",
	}
	return rc
}

// TestRun_emptyActiveAccount_returnsEmptyPayload is the tracer bullet:
// when the active Account has no registered packages, Run returns an
// empty Payload. The empty-state UX (placeholder message) is owned by
// each renderer rather than Run itself: see
// TestPayload_table_empty_rendersPlaceholderOnStdout and the markdown
// equivalent, so `gplay apps list --output markdown 2>&1` no longer
// double-prints the empty notice.
func TestRun_emptyActiveAccount_returnsEmptyPayload(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := newRC(t, &stdout, &stderr, nil)

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p, ok := r.(listcmd.Payload)
	if !ok {
		t.Fatalf("Run returned %T, want listcmd.Payload", r)
	}
	if len(p.Apps) != 0 {
		t.Errorf("empty registry should produce zero rows; got %+v", p.Apps)
	}
}

// TestRun_withPackages_returnsRowsUnpinned exercises the happy path
// without any project pin: every row should carry Pinned=false. The
// row order mirrors the registry's insertion order so the user-visible
// list is stable.
func TestRun_withPackages_returnsRowsUnpinned(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := newRC(t, &stdout, &stderr, []string{"com.example.alpha", "com.example.beta"})

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p, ok := r.(listcmd.Payload)
	if !ok {
		t.Fatalf("Run returned %T, want listcmd.Payload", r)
	}
	if len(p.Apps) != 2 {
		t.Fatalf("len(Apps) = %d, want 2", len(p.Apps))
	}
	if p.Apps[0].Package != "com.example.alpha" || p.Apps[0].Pinned {
		t.Errorf("Apps[0] = %+v, want {com.example.alpha, false}", p.Apps[0])
	}
	if p.Apps[1].Package != "com.example.beta" || p.Apps[1].Pinned {
		t.Errorf("Apps[1] = %+v, want {com.example.beta, false}", p.Apps[1])
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty when there are packages; got %q", stderr.String())
	}
}

// TestRun_marksPinnedRow asserts the central display feature: the row
// whose package matches rc.Resolved.Pin (the repo's
// .gplay/config.json pin) is marked Pinned=true; siblings stay false.
func TestRun_marksPinnedRow(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := newRC(t, &stdout, &stderr, []string{"com.example.alpha", "com.example.beta"})
	rc.Resolved.Pin = "com.example.beta"

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p := r.(listcmd.Payload)
	if p.Apps[0].Pinned {
		t.Errorf("Apps[0] (alpha) should not be pinned; got Pinned=true")
	}
	if !p.Apps[1].Pinned {
		t.Errorf("Apps[1] (beta) should be pinned; got Pinned=false")
	}
}

// TestRun_accountFlagOverride_listsAccountThatActuallyResolved asserts
// the central account-resolution invariant: when --account / GPLAY_ACCOUNT
// pick a credential other than the cascade's active, list MUST scope to
// the resolver's choice (rc.AccountName), not to rc.Resolved.ConfigAccount.
// Mirrors addcmd's TestRun_persistsUnderAccountThatActuallyRanProbe: the
// same pattern that locked addcmd's fix in place.
func TestRun_accountFlagOverride_listsAccountThatActuallyResolved(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: &stdout,
		Stderr: &stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	// Simulate --account altname overriding the cascade: AccountName is
	// the resolver's choice; ConfigAccount stays "playci" (the global
	// Active flag, which the user did NOT switch).
	rc.AccountName = "altname"
	rc.Resolved = &config.Resolved{
		Accounts: []config.Account{
			{Name: "playci", Active: true, Packages: []string{"com.playci.app"}},
			{Name: "altname", Packages: []string{"com.altname.app"}},
		},
		ConfigAccount: "playci",
	}

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p := r.(listcmd.Payload)
	if len(p.Apps) != 1 || p.Apps[0].Package != "com.altname.app" {
		t.Errorf("Apps = %+v; want exactly [com.altname.app] (the --account override target)", p.Apps)
	}
}

// TestRun_inlineCredential_returnsClearError asserts the
// --service-account / GPLAY_SERVICE_ACCOUNT branch: when a credential
// resolved but no local Account name is attached (inline credentials are
// per-invocation and have no name to scope the registry to), list MUST
// return a clear error telling the user the limitation, NOT a misleading
// "run gplay auth login" hint that ignores the credential they just
// supplied. Mirrors addcmd.go:85-87.
func TestRun_inlineCredential_returnsClearError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: &stdout,
		Stderr: &stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = &serviceaccount.ServiceAccount{} // non-nil → a credential resolved
	rc.AccountName = ""                           // inline: no local name
	rc.Resolved = &config.Resolved{}              // ConfigAccount empty

	_, err := listcmd.Run(rc, listcmd.Input{})
	if err == nil {
		t.Fatal("Run: expected error for inline credential, got nil")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For = %d, want 2 (CLI misuse); err = %v", got, err)
	}
	if !strings.Contains(err.Error(), "inline credential") && !strings.Contains(err.Error(), "--service-account") {
		t.Errorf("error should explain the inline-credential limitation; got %q", err.Error())
	}
}

// TestRun_accountNotInGlobal_returnsDistinctError asserts the
// missing-account branch: when the resolved Account name is not present
// in rc.Resolved.Accounts (race with `gplay auth logout`, or a stale
// .gplay/config.local.json pointing at a removed Account), list MUST
// return a distinct error telling the user the Account itself is gone,
// NOT the empty-registry hint that points at `gplay apps add` (which
// would itself fail with ErrUnknownAccount).
func TestRun_accountNotInGlobal_returnsDistinctError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: &stdout,
		Stderr: &stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.AccountName = "ghost"
	rc.Resolved = &config.Resolved{
		Accounts:      []config.Account{{Name: "playci", Active: true}}, // ghost not in Accounts
		ConfigAccount: "playci",
	}

	_, err := listcmd.Run(rc, listcmd.Input{})
	if err == nil {
		t.Fatal("Run: expected error for missing-Account, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing Account; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "gplay auth login") {
		t.Errorf("error should point at `gplay auth login`; got %q", err.Error())
	}
}

// TestRun_accountScope_ignoresPackagesUnderOtherAccounts asserts the
// per-Account isolation that makes the registry useful for multi-app
// maintainers: switching the active Account changes the visible
// packages even when other Accounts have their own. A bug here (e.g.
// flattening across Accounts) would silently leak one Account's
// inventory into another's display.
func TestRun_accountScope_ignoresPackagesUnderOtherAccounts(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: &stdout,
		Stderr: &stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.AccountName = "playci"
	rc.Resolved = &config.Resolved{
		Accounts: []config.Account{
			{Name: "playci", Active: true, Packages: []string{"com.playci.app"}},
			{Name: "altname", Packages: []string{"com.altname.app"}},
		},
		ConfigAccount: "playci",
	}

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p := r.(listcmd.Payload)
	if len(p.Apps) != 1 || p.Apps[0].Package != "com.playci.app" {
		t.Errorf("Apps = %+v; want exactly [com.playci.app]", p.Apps)
	}
}

// TestRun_noCredentialAtAll_returnsAuthErrorWithHint asserts the
// nothing-resolved case: no --account, no --service-account, no env,
// no cascade active: Run returns an error carrying exit code 10 (no
// credential resolved) and the message points at `gplay auth login`.
// Distinct from the inline-credential case (rc.Account != nil, exit 2)
// and the missing-account case (Account name set but not in registry).
func TestRun_noCredentialAtAll_returnsAuthErrorWithHint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(context.Background(), kernel.Boot{
		Stdout: &stdout,
		Stderr: &stderr,
	}, kernel.Inputs{Format: output.FormatJSON})
	rc.Resolved = &config.Resolved{} // no Accounts, no ConfigAccount, no rc.AccountName, no rc.Account

	_, err := listcmd.Run(rc, listcmd.Input{})
	if err == nil {
		t.Fatal("Run: expected error for missing active Account, got nil")
	}
	if got := exit.For(err); got != 10 {
		t.Errorf("exit.For = %d, want 10 (no credential); err = %v", got, err)
	}
	if !strings.Contains(err.Error(), "gplay auth login") {
		t.Errorf("error should point at `gplay auth login`; got %q", err.Error())
	}
}

// TestPayload_jsonShape asserts the wire shape consumers depend on:
// `{"apps":[{"package":"...","pinned":true}]}`. Renaming a field or
// changing the case here is a breaking change for scripts piping
// `gplay apps list --output json` into jq.
func TestPayload_jsonShape(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{
		{Package: "com.example.alpha", Pinned: true},
		{Package: "com.example.beta"},
	}}
	var buf bytes.Buffer
	if err := p.Renderers().JSON(&buf); err != nil {
		t.Fatalf("Renderers().JSON: %v", err)
	}
	var parsed struct {
		Apps []struct {
			Package string `json:"package"`
			Pinned  bool   `json:"pinned"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, buf.String())
	}
	if len(parsed.Apps) != 2 {
		t.Fatalf("len(Apps) = %d, want 2", len(parsed.Apps))
	}
	if parsed.Apps[0].Package != "com.example.alpha" || !parsed.Apps[0].Pinned {
		t.Errorf("Apps[0] = %+v, want alpha pinned=true", parsed.Apps[0])
	}
	if parsed.Apps[1].Package != "com.example.beta" || parsed.Apps[1].Pinned {
		t.Errorf("Apps[1] = %+v, want beta pinned=false", parsed.Apps[1])
	}
}

// TestPayload_table_empty_rendersPlaceholderOnStdout asserts the empty-
// state UX contract: when there are no rows, the table renderer prints
// a visible placeholder on stdout (matching commands/auth/list/list.go).
// The previous behavior (render nothing) left a TTY user staring at a
// blank screen when stderr was redirected.
func TestPayload_table_empty_rendersPlaceholderOnStdout(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{}}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Renderers().Table: %v", err)
	}
	if !strings.Contains(buf.String(), "no packages registered") {
		t.Errorf("empty table should emit a placeholder; got %q", buf.String())
	}
}

// TestRun_emptyActiveAccount_writesPlaceholderViaRenderer_notStderr
// asserts that the empty-list hint moves from stderr into the renderer
// itself. The previous shape (stderr hint + markdown placeholder)
// duplicated the message in `gplay apps list --output markdown 2>&1`;
// the new shape funnels the empty notice through whichever renderer is
// active, so each format owns its own empty-state UX.
func TestRun_emptyActiveAccount_writesPlaceholderViaRenderer_notStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rc := newRC(t, &stdout, &stderr, nil)

	if _, err := listcmd.Run(rc, listcmd.Input{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty after the renderer refactor (empty-state lives in renderers); got %q", stderr.String())
	}
}

// TestPayload_tableHeadersAndPinMarker asserts that the table renderer
// emits the PACKAGE / PINNED columns and that the pinned row carries a
// visible marker (the body of every other row in the same column stays
// empty). Asserting on individual cells rather than exact alignment
// makes the test resilient to tabwriter spacing tweaks.
func TestPayload_tableHeadersAndPinMarker(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{
		{Package: "com.example.alpha", Pinned: false},
		{Package: "com.example.beta", Pinned: true},
	}}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Renderers().Table: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"PACKAGE", "PINNED", "com.example.alpha", "com.example.beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q; got:\n%s", want, out)
		}
	}
	// The pinned row must carry a non-empty marker in the PINNED column;
	// the unpinned row must NOT carry that same marker.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("table should be header + 2 rows; got %d lines:\n%s", len(lines), out)
	}
	alphaLine, betaLine := lines[1], lines[2]
	pinMarker := "✓"
	if strings.Contains(alphaLine, pinMarker) {
		t.Errorf("unpinned alpha row carries the pin marker %q: %q", pinMarker, alphaLine)
	}
	if !strings.Contains(betaLine, pinMarker) {
		t.Errorf("pinned beta row missing the pin marker %q: %q", pinMarker, betaLine)
	}
}

// runCmd executes a fresh `gplay apps list` cobra tree with stdout/
// stderr piped into the caller's buffers. The boot points at a fresh
// tempdir-backed config.json (cfgPath) so seed() can write a global
// config the kernel will discover during buildRunContext.
func runCmd(t *testing.T, cfgPath string, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	boot := kernel.Boot{
		Stdout:     stdout,
		Stderr:     stderr,
		ConfigPath: cfgPath,
	}
	sub := listcmd.NewCommand(boot)
	root := &cobra.Command{Use: "gplay"}
	root.AddCommand(sub)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"list"}, args...))
	return root.Execute()
}

// seedGlobal writes a global config.json with one active Account named
// "playci" carrying the given packages. Returns the path the test
// should hand to runCmd via boot.ConfigPath.
func seedGlobal(t *testing.T, packages []string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	g := &config.Global{Accounts: []config.Account{{Name: "playci", Active: true, Packages: packages}}}
	if err := g.Save(context.Background(), config.OSFS{}, cfgPath); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return cfgPath
}

// TestList_cobra_extraArg_rejectedByNoArgs asserts that positional
// arguments are explicitly rejected: `gplay apps list com.example.foo`
// (typo for `apps add`, or a stale --filter expectation) must NOT be
// silently accepted. Without Args: cobra.NoArgs the default is
// ArbitraryArgs and the typo would print a normal listing: making the
// user believe their argument took effect.
func TestList_cobra_extraArg_rejectedByNoArgs(t *testing.T) {
	cfgPath := seedGlobal(t, nil)
	var stdout, stderr bytes.Buffer
	err := runCmd(t, cfgPath, &stdout, &stderr, "com.example.foo")
	if err == nil {
		t.Fatal("Execute: expected error for unexpected positional arg, got nil")
	}
	if !strings.Contains(err.Error(), "unknown command") && !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "arg") {
		t.Errorf("error should mention unknown command/argument; got %q", err.Error())
	}
}

// TestList_cobra_emptyRegistry_jsonStaysParseable asserts the pipe-
// friendliness invariant: the empty-list path produces a valid `{"apps":
// []}` on stdout and writes nothing to stderr: scripts doing
// `gplay apps list --output json | jq '.apps | length'` get 0, not an
// error. The empty-state hint lives in the table/markdown renderers, not
// in the JSON path, so machine-readable output stays pristine.
func TestList_cobra_emptyRegistry_jsonStaysParseable(t *testing.T) {
	cfgPath := seedGlobal(t, nil) // empty registry
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, cfgPath, &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed struct {
		Apps []struct{} `json:"apps"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout should be valid JSON; got %q (err=%v)", stdout.String(), err)
	}
	if len(parsed.Apps) != 0 {
		t.Errorf("Apps should be empty; got %+v", parsed.Apps)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty in the JSON path; got %q", stderr.String())
	}
}

// TestList_cobra_jsonOutput_passThroughShape is the cobra-level wiring
// test: a real `gplay apps list --output json` against a seeded global
// config. It proves NewCommand binds --output, that the kernel resolves
// it, and that the JSON renderer is reached with a Payload populated
// from the registry. Skipping this test would let a broken cobra wire
// pass every Run unit test.
func TestList_cobra_jsonOutput_passThroughShape(t *testing.T) {
	cfgPath := seedGlobal(t, []string{"com.example.alpha", "com.example.beta"})
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, cfgPath, &stdout, &stderr, "--output", "json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed struct {
		Apps []struct {
			Package string `json:"package"`
			Pinned  bool   `json:"pinned"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, stdout.String())
	}
	if len(parsed.Apps) != 2 {
		t.Fatalf("len(Apps) = %d, want 2", len(parsed.Apps))
	}
	if parsed.Apps[0].Package != "com.example.alpha" || parsed.Apps[1].Package != "com.example.beta" {
		t.Errorf("unexpected packages: %+v", parsed.Apps)
	}
}

// TestPayload_markdownTable asserts the markdown renderer produces a
// GFM-style table with the same Package / Pinned columns. Asserting on
// header line + separator + a pinned + unpinned row keeps the test
// resilient to cosmetic spacing while pinning the wire shape.
func TestPayload_markdownTable(t *testing.T) {
	p := listcmd.Payload{Apps: []listcmd.AppRow{
		{Package: "com.example.alpha", Pinned: false},
		{Package: "com.example.beta", Pinned: true},
	}}
	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Renderers().Markdown: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"| Package | Pinned |",
		"| --- | --- |",
		"| com.example.alpha |  |",
		"| com.example.beta | ✓ |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q; got:\n%s", want, out)
		}
	}
}

// TestList_cobra_malformedInlineCredential_exits10NotFallback drives the
// real cobra command through the production lazy path with a malformed
// inline GPLAY_SERVICE_ACCOUNT. The AccountName == "" branch must surface
// the invalid-credential error (exit 10, the real cause) instead of
// silently falling back to ConfigAccount and listing the cascade Account's
// packages (#180, ADR-0020).
func TestList_cobra_malformedInlineCredential_exits10NotFallback(t *testing.T) {
	t.Setenv(resolver.EnvAccount, "")
	t.Setenv(resolver.EnvServiceAccount, "{ not valid json")
	cfgPath := seedGlobal(t, []string{"com.cascade.app"})
	var stdout, stderr bytes.Buffer

	err := runCmd(t, cfgPath, &stdout, &stderr, "--output", "json")
	if err == nil {
		t.Fatal("apps list with a malformed inline credential = nil, want exit 10")
	}
	if got := exit.For(err); got != 10 {
		t.Errorf("exit.For = %d, want 10; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "could not read credential") {
		t.Errorf("error should name the real cause; got %q", err.Error())
	}
	// Lock in the cause-preservation contract (%w): the wrapped JSON parse
	// error survives, not just the wrapper prefix.
	if !strings.Contains(err.Error(), "invalid character") {
		t.Errorf("error should preserve the underlying JSON parse cause; got %q", err.Error())
	}
	if strings.Contains(stdout.String(), "com.cascade.app") {
		t.Errorf("a malformed inline credential must not list the cascade Account's packages; got %q", stdout.String())
	}
	// Under --output json a failure emits the structured error envelope on
	// stdout (ADR-0023). It carries only the error (exit 10 + the real cause)
	// (never a data payload) so the cascade leak guarded above still cannot
	// appear here; assert the envelope rather than an empty stdout.
	if out := stdout.String(); !strings.Contains(out, `"exitCode": 10`) {
		t.Errorf("json hard-error path should emit the exit-10 error envelope; got %q", out)
	}
}
