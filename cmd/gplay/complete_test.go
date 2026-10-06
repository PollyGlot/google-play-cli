package main

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestCompletion_afterASingleValueFlag drives the shell's `__complete` request
// through the real tree. cobra parses the flags of a completion request twice,
// so the repeated-flag guard (PRD #446) saw `--package` set twice and turned
// every completion that follows a single-value flag into an error. The
// release filter options must come back as suggestions (#348).
func TestCompletion_afterASingleValueFlag(t *testing.T) {
	t.Setenv(resolver.EnvAccount, "")
	t.Setenv(resolver.EnvServiceAccount, string(testkit.ServiceAccountJSON(t)))
	fake := testkit.NewFake(testkit.Any(http.StatusOK,
		`{"tracks":[{"displayName":"Production","type":"PRODUCTION","servingReleases":[{"displayName":"1.4.0","versionCodes":["141"]}]}]}`))
	dir := t.TempDir()
	root := newRootCmd(kernel.Boot{ConfigPath: filepath.Join(dir, "config.json"), KeystoreRoot: filepath.Join(dir, "accounts")})
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	args := []string{cobra.ShellCompRequestCmd, "vitals", "crashes", "--package", "com.example.app", "--version-code", ""}
	prepareCompletion(root, args)
	root.SetArgs(args)

	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: fake})
	if code := execute(ctx, root, &errb, nil); code != 0 {
		t.Fatalf("exit %d; stderr: %s", code, errb.String())
	}
	if want := "141\t1.4.0 (Production)\n:4\n"; out.String() != want {
		t.Errorf("completion output = %q, want %q; stderr: %s", out.String(), want, errb.String())
	}
	if strings.Contains(errb.String(), "repeated flag") {
		t.Errorf("the completion re-parse tripped the repeated-flag guard: %s", errb.String())
	}
}
