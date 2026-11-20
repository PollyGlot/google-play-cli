package kernel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/redact"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

func GroupRunE(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if r, ok := removedVerb(cmd, args); ok {
		return removedVerbError(cmd, r)
	}
	return exit.Usagef("unknown command %q for %q%s", args[0], cmd.CommandPath(), suggestionsFor(cmd, args[0]))
}

func Group(use, short string, children ...*cobra.Command) *cobra.Command {
	g := &cobra.Command{
		Use:           use,
		Short:         short,
		RunE:          GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	g.AddCommand(children...)
	return g
}

func suggestionsFor(cmd *cobra.Command, typed string) string {
	if cmd.DisableSuggestions {
		return ""
	}
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	suggestions := cmd.SuggestionsFor(typed)
	if len(suggestions) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\nDid you mean this?")
	for _, s := range suggestions {
		fmt.Fprintf(&sb, "\n    %s", s)
	}
	return sb.String()
}

const mutatingAnnotation = "gplay.mutating"

func MarkMutating(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[mutatingAnnotation] = "true"
	return cmd
}

func IsMutating(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Annotations[mutatingAnnotation] == "true"
}

const scopeAnnotation = "gplay.scope"

func WithScope(cmd *cobra.Command, oauthScope string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[scopeAnnotation] = oauthScope
	return cmd
}

func ScopeFor(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	return cmd.Annotations[scopeAnnotation]
}

const EnvReadonly = "GPLAY_READONLY"

func envReadonlyEnforced() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvReadonly))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

type Boot struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	FS config.FS

	ConfigPath string

	KeystoreRoot string

	Keyring keystore.KeyringAPI
}

type Inputs struct {
	Ctx context.Context

	Format output.Format

	Resolver resolver.Inputs

	Verbose bool

	Timeout time.Duration

	Retry int

	Mutating bool

	Preview bool

	Readonly bool

	Scope string
}

type RunContext struct {
	Ctx context.Context

	Account *serviceaccount.ServiceAccount

	AccountName string

	Format output.Format

	Resolved *config.Resolved

	Keystore      keystore.Backend
	KeystoreLabel string

	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	FS config.FS

	Verbose bool

	Timeout time.Duration

	Retry int

	Scope string

	ConfigPath   string
	KeystoreRoot string

	lazy                bool
	keyring             keystore.KeyringAPI
	resolverInputs      resolver.Inputs
	backendDone         bool
	accountDone         bool
	outCounter          *countingWriter
	accountErr          error
	controlPlaneDefault time.Duration
}

func NewForTest(ctx context.Context, boot Boot, in Inputs) *RunContext {
	fsys := boot.FS
	if fsys == nil {
		fsys = config.OSFS{}
	}
	stdout := boot.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := redact.Writer(boot.Stderr)
	return &RunContext{
		Ctx:          ctx,
		Stdout:       stdout,
		Stderr:       stderr,
		Stdin:        boot.Stdin,
		FS:           fsys,
		Verbose:      in.Verbose,
		Timeout:      in.Timeout,
		Retry:        in.Retry,
		Scope:        in.Scope,
		ConfigPath:   boot.ConfigPath,
		KeystoreRoot: boot.KeystoreRoot,
		Format:       in.Format,
	}
}

func Run(boot Boot, in Inputs, fn func(*RunContext) (output.Renderable, error)) error {
	rc, err := buildRunContext(boot, in)
	if err != nil {
		return err
	}
	if in.Readonly && in.Mutating && !in.Preview {
		refusal := exit.Policyf(
			"refused by %s: this command mutates Google Play state and is blocked by the read-only environment policy. This is not resolvable by adding a flag: unset %s to allow writes, or run with --dry-run (or --plan where offered) to preview.",
			EnvReadonly, EnvReadonly)
		rc.maybeWriteErrorEnvelope(refusal)
		return refusal
	}
	payload, runErr := fn(rc)
	if runErr != nil {
		rc.maybeWriteErrorEnvelope(runErr)
		return runErr
	}
	if payload == nil {
		return nil
	}
	return output.Render(rc.Stdout, rc.Format, payload.Renderers())
}

func (rc *RunContext) Confirmf(format string, args ...any) {
	rc.log().Confirmf(format, args...)
}

func (rc *RunContext) Warnf(format string, args ...any) {
	rc.log().Warnf(format, args...)
}

func (rc *RunContext) WarnTruncated(n int, what, flag string) {
	rc.Warnf("results truncated to %d %s by --%s: more were available; raise --%s or drop it to get everything",
		n, what, flag, flag)
}

func (rc *RunContext) ConfirmMutation(explicitEditID, format string, args ...any) {
	if explicitEditID == "" {
		rc.Confirmf(format, args...)
		return
	}
	prefix := fmt.Sprintf("• staged in open edit %s: run `gplay edits commit` to publish (not live yet): ", explicitEditID)
	rc.log().Logf(prefix+format, args...)
}

func (rc *RunContext) GplayDir() (string, bool) {
	if rc.Resolved == nil || rc.Resolved.ProjectSharedPath == "" {
		return "", false
	}
	return filepath.Dir(rc.Resolved.ProjectSharedPath), true
}

func (rc *RunContext) ExplicitEditID(pkg string) (string, error) {
	gplayDir, ok := rc.GplayDir()
	if !ok || rc.FS == nil {
		return "", nil
	}
	pin, found, err := editpin.Lookup(rc.FS, gplayDir, pkg)
	if err != nil || !found {
		return "", err
	}
	return pin.EditID, nil
}

func (rc *RunContext) maybeWriteErrorEnvelope(err error) {
	if rc.Format != output.FormatJSON {
		return
	}
	if rc.outCounter != nil && rc.outCounter.n > 0 {
		return
	}
	_ = output.WriteErrorEnvelope(rc.Stdout, err)
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func RunCobra(cmd *cobra.Command, boot Boot, outputFlag string, fn func(*RunContext) (output.Renderable, error)) error {
	boot.Stdout = cmd.OutOrStdout()
	boot.Stderr = cmd.ErrOrStderr()
	return Run(boot, FromCobra(cmd, outputFlag), fn)
}

func buildRunContext(boot Boot, in Inputs) (*RunContext, error) {
	fsys := boot.FS
	if fsys == nil {
		fsys = config.OSFS{}
	}
	ctx := in.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	stdout := boot.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := redact.Writer(boot.Stderr)

	format, err := output.Resolve(in.Format, stdout)
	if err != nil {
		return nil, err
	}

	resolved, err := config.LoadFromEnv(ctx, fsys, boot.ConfigPath)
	if err != nil {
		return nil, err
	}

	kr := boot.Keyring
	if kr == nil {
		kr = keystore.DefaultKeyring()
	}

	accountName := resolver.ResolveName(resolver.Deps{Resolved: resolved}, in.Resolver)

	cw := &countingWriter{w: stdout}

	rc := &RunContext{
		Ctx:            ctx,
		AccountName:    accountName,
		Format:         format,
		Resolved:       resolved,
		Stdout:         cw,
		outCounter:     cw,
		Stderr:         stderr,
		Stdin:          boot.Stdin,
		FS:             fsys,
		Verbose:        in.Verbose,
		Timeout:        in.Timeout,
		Retry:          in.Retry,
		Scope:          in.Scope,
		ConfigPath:     boot.ConfigPath,
		KeystoreRoot:   boot.KeystoreRoot,
		lazy:           true,
		keyring:        kr,
		resolverInputs: in.Resolver,
	}
	warnTrackedLocalConfig(rc)
	warnForeignProject(rc)
	return rc, nil
}

func warnForeignProject(rc *RunContext) {
	if rc.Resolved == nil || rc.Resolved.ForeignProjectDir == "" {
		return
	}
	rc.Warnf("ignoring %s: another user owns it or every user can write to it, so its package, Account, developer id and Edit pin are not used. If the project is yours, take ownership of it (chown) and remove write access for others (chmod o-w); otherwise pass --package and --account",
		rc.Resolved.ForeignProjectDir)
}

func warnTrackedLocalConfig(rc *RunContext) {
	if rc.Resolved == nil || rc.Resolved.ProjectLocalPath == "" {
		return
	}
	if !config.GitTracked(rc.Ctx, rc.Resolved.ProjectLocalPath) {
		return
	}
	rc.Warnf("%s is tracked by git, and it picks the Account gplay runs with: anyone who clones the repo runs with the Account it names. Untrack it: git rm --cached %s",
		rc.Resolved.ProjectLocalPath, rc.Resolved.ProjectLocalPath)
}

func (rc *RunContext) Backend() (keystore.Backend, error) {
	if !rc.lazy || rc.backendDone {
		return rc.Keystore, nil
	}
	be, label, err := keystore.Select(rc.Ctx, keystore.SelectOptions{
		Keyring:  rc.keyring,
		FileRoot: rc.KeystoreRoot,
	})
	if err != nil {
		return nil, err
	}
	rc.Keystore = be
	rc.KeystoreLabel = label
	rc.backendDone = true
	if rc.Verbose {
		keystore.LogBackend(rc.Stderr, label)
	}
	return be, nil
}

func (rc *RunContext) EnsureAccount() error {
	if !rc.lazy || rc.accountDone {
		return rc.accountErr
	}
	rc.accountDone = true
	sa, err := resolver.Resolve(rc.Ctx, resolver.Deps{
		Resolved: rc.Resolved,
		Keystore: resolverKeystore{rc: rc},
	}, rc.resolverInputs)
	if err != nil {
		if errors.Is(err, resolver.ErrNoSource) || errors.Is(err, keystore.ErrNotFound) {
			return nil
		}
		rc.accountErr = &credentialError{cause: err}
		return rc.accountErr
	}
	rc.Account = sa
	return nil
}

type credentialError struct{ cause error }

func (e *credentialError) Error() string { return "could not read credential: " + e.cause.Error() }
func (e *credentialError) ExitCode() int { return 10 }
func (e *credentialError) Unwrap() error { return e.cause }

type resolverKeystore struct{ rc *RunContext }

func (k resolverKeystore) Load(ctx context.Context, name string) ([]byte, error) {
	be, err := k.rc.Backend()
	if err != nil {
		return nil, err
	}
	return be.Load(ctx, name)
}

func (k resolverKeystore) Save(ctx context.Context, name string, data []byte) error {
	be, err := k.rc.Backend()
	if err != nil {
		return err
	}
	return be.Save(ctx, name, data)
}

func (k resolverKeystore) Delete(ctx context.Context, name string) error {
	be, err := k.rc.Backend()
	if err != nil {
		return err
	}
	return be.Delete(ctx, name)
}

func (k resolverKeystore) List(ctx context.Context) ([]string, error) {
	be, err := k.rc.Backend()
	if err != nil {
		return nil, err
	}
	return be.List(ctx)
}

const defaultControlPlaneTimeout = 60 * time.Second

func (rc *RunContext) AuthedClient() (*http.Client, error) {
	return rc.authedClient(rc.controlPlaneTimeout())
}

func (rc *RunContext) UploadClient() (*http.Client, error) {
	if rc.Timeout > 0 {
		return rc.authedClient(rc.Timeout)
	}
	return rc.authedClientFor(rc.defaultTimeout(), true)
}

func (rc *RunContext) SetControlPlaneDefaultForTest(d time.Duration) {
	rc.controlPlaneDefault = d
}

func (rc *RunContext) defaultTimeout() time.Duration {
	if rc.controlPlaneDefault > 0 {
		return rc.controlPlaneDefault
	}
	return defaultControlPlaneTimeout
}

func (rc *RunContext) PublicClient() *http.Client {
	timeout := rc.controlPlaneTimeout()
	rt := transport.HTTPSOnly(baseHTTPClient(rc.Ctx).Transport)
	if rc.Retry > 0 {
		rt = transport.WithRetry(rt, transport.RetryOptions{MaxRetries: rc.Retry, Timeout: timeout})
		timeout = 0
	}
	return transport.NewClient(rt, timeout)
}

func (rc *RunContext) scopes() []string {
	if rc.Scope == "" {
		return nil
	}
	return []string{rc.Scope}
}

func (rc *RunContext) controlPlaneTimeout() time.Duration {
	if rc.Timeout > 0 {
		return rc.Timeout
	}
	return rc.defaultTimeout()
}

func (rc *RunContext) authedClient(timeout time.Duration) (*http.Client, error) {
	return rc.authedClientFor(timeout, false)
}

func (rc *RunContext) authedClientFor(timeout time.Duration, mediaExempt bool) (*http.Client, error) {
	if err := rc.RequireAccount(); err != nil {
		return nil, err
	}
	base := baseHTTPClient(rc.Ctx)
	timedBase := transport.NewClient(base.Transport, timeout)
	ctx := context.WithValue(rc.Ctx, oauth2.HTTPClient, timedBase)
	ts, err := token.Source(ctx, rc.Account, rc.scopes()...)
	if err != nil {
		return nil, &authError{msg: "could not build token source: " + err.Error()}
	}
	client := oauth2.NewClient(ctx, ts)
	if mediaExempt {
		client.Transport = transport.WithRetry(
			transport.WithControlPlaneDeadline(client.Transport, timeout),
			transport.RetryOptions{MaxRetries: rc.Retry},
		)
		client.Timeout = 0
		return client, nil
	}
	if rc.Retry > 0 {
		client.Transport = transport.WithRetry(client.Transport, transport.RetryOptions{
			MaxRetries: rc.Retry,
			Timeout:    timeout,
		})
		client.Timeout = 0
		return client, nil
	}
	client.Timeout = timeout
	return client, nil
}

func baseHTTPClient(ctx context.Context) *http.Client {
	if v := ctx.Value(oauth2.HTTPClient); v != nil {
		if c, ok := v.(*http.Client); ok && c != nil {
			return c
		}
	}
	return http.DefaultClient
}

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
func (e *authError) ExitCode() int { return 10 }

const noAccountMsg = "no Account resolved: run `gplay auth login`, pass --account <name> or set GPLAY_ACCOUNT, " +
	"or supply a service-account key with --service-account or GPLAY_SERVICE_ACCOUNT"

func NoAccountError() error { return &authError{msg: noAccountMsg} }

func (rc *RunContext) RequireAccount() error {
	if err := rc.EnsureAccount(); err != nil {
		return err
	}
	if rc.Account == nil {
		return NoAccountError()
	}
	return nil
}

func FromCobra(cmd *cobra.Command, format string) Inputs {
	verbose, _ := cmd.Flags().GetBool("verbose")
	saFlag, _ := cmd.Flags().GetString("service-account")
	acctFlag, _ := cmd.Flags().GetString("account")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	retry, _ := cmd.Flags().GetInt("retry")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	plan, _ := cmd.Flags().GetBool("plan")
	return Inputs{
		Ctx:      cmd.Context(),
		Format:   output.Format(format),
		Verbose:  verbose,
		Timeout:  timeout,
		Retry:    retry,
		Mutating: IsMutating(cmd),
		Preview:  dryRun || plan,
		Readonly: envReadonlyEnforced(),
		Scope:    ScopeFor(cmd),
		Resolver: resolver.Inputs{
			ServiceAccountFlag: saFlag,
			AccountFlag:        acctFlag,
			EnvServiceAccount:  os.Getenv(resolver.EnvServiceAccount),
			EnvAccount:         os.Getenv(resolver.EnvAccount),
		},
	}
}
