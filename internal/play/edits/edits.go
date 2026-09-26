// Package edits owns the Google Play Edit transactional lifecycle. The
// canonical entrypoint is WithEdit: open an Edit on a package, invoke a
// caller-supplied closure with the new Edit ID, and commit on success.
// Auto-discard on closure failure (and the KeepOnFailure opt-out) land
// in Block 2 of the TDD plan; for the tracer bullet, the happy path
// is enough.
package edits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// Registry entries this package calls. Resolving them at init makes an
// unregistered or vanished method a startup panic caught by CI (the registry
// tests resolve every entry) rather than a runtime surprise; verb and URL then
// come from the Discovery snapshot instead of literals kept here (#513).
var (
	methodInsert   = apiregistry.MustResolve("androidpublisher.edits.insert")
	methodDelete   = apiregistry.MustResolve("androidpublisher.edits.delete")
	methodCommit   = apiregistry.MustResolve("androidpublisher.edits.commit")
	methodGet      = apiregistry.MustResolve("androidpublisher.edits.get")
	methodValidate = apiregistry.MustResolve("androidpublisher.edits.validate")
)

// DanglingEditError wraps the upstream failure that caused an Edit to be
// left open in KeepOnFailure mode. It carries the Edit ID so the
// operator can recover by waiting for the Edit's ~24h expiry, or by
// releasing it via the Google Play Console. (The explicit `gplay edits`
// lifecycle exists now, but an Edit dangling from an IMPLICIT-mode failure
// was never pinned in .gplay/, so `gplay edits discard` cannot find it:
// the message points at the remediation paths that do apply.) It implements
// gplay's Coder contract by inheriting from the wrapped error when possible,
// falling back to exit 60 (state conflict) so the dangling Edit
// surfaces as a retryable state condition.
type DanglingEditError struct {
	EditID string
	Err    error
}

func (e *DanglingEditError) Error() string {
	return fmt.Sprintf("edit %s left open (wait ~24h for expiry, or release it via the Google Play Console): %v", e.EditID, e.Err)
}

func (e *DanglingEditError) Unwrap() error { return e.Err }

func (e *DanglingEditError) ExitCode() int {
	var c interface{ ExitCode() int }
	if errors.As(e.Err, &c) {
		return c.ExitCode()
	}
	return 60
}

// StalePinError wraps a write that failed because the explicit Edit pinned in
// .gplay/edit-<package>.json no longer exists server-side (it expired, or was
// committed or discarded by another client). Without it the user sees a bare
// 404 from, say, tracks.update, with nothing saying a local pin silently
// redirected the command to a dead Edit. The message names the pin and the
// verb that clears it; the exit code and diagnostic code stay those of the
// wrapped API error, so the frozen exit-code contract does not move.
type StalePinError struct {
	Package string
	EditID  string
	Err     error
}

func (e *StalePinError) Error() string {
	return fmt.Sprintf("%v: the command ran against explicit edit %s pinned in .gplay/%s, which no longer exists (expired, or committed or discarded elsewhere); run `gplay edits discard --package %s` to clear the pin, then retry",
		e.Err, e.EditID, editpin.FileName(e.Package), e.Package)
}

func (e *StalePinError) Unwrap() error { return e.Err }

// ExitCode forwards to the wrapped error, falling back to 30 (the 404 bucket
// the wrapped failure comes from).
func (e *StalePinError) ExitCode() int {
	var c interface{ ExitCode() int }
	if errors.As(e.Err, &c) {
		return c.ExitCode()
	}
	return 30
}

// explainStalePin decides whether an explicit-mode failure is the pinned Edit
// having vanished, and wraps it in a *StalePinError when it is. An editExpired
// reason says so outright. A bare 404 is ambiguous (the Edit, or a resource
// inside it such as a track not created yet, whose own hint must not be
// drowned), so it is settled by one edits.get on the pinned id: the same probe
// `gplay edits status --live` uses. Any other failure, or a probe that finds
// the Edit alive or cannot tell, returns err untouched.
func explainStalePin(ctx context.Context, hc *http.Client, pkg, editID string, err error) error {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	stale := &StalePinError{Package: pkg, EditID: editID, Err: err}
	if hasReason(apiErr, "editExpired") {
		return stale
	}
	if apiErr.StatusCode != http.StatusNotFound {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _, probeErr := GetExplicit(probeCtx, hc, pkg, editID)
	var probe *api.Error
	if errors.As(probeErr, &probe) && (probe.StatusCode == http.StatusNotFound || hasReason(probe, "editExpired")) {
		return stale
	}
	return err
}

// Options tunes the Edit lifecycle. KeepOnFailure suppresses the
// auto-discard cleanup when the closure returns an error: see Block 2.
//
// ExplicitEditID selects the explicit-mode (`gplay edits begin/commit/discard`)
// contract: when it is non-empty, WithEdit runs fn against that already-open
// Edit WITHOUT opening, committing, or discarding one: the caller owns the
// lifecycle. KeepOnFailure is moot in that mode (nothing is auto-discarded),
// and so is Commit: the pinned Edit is committed by `gplay edits commit`.
//
// Commit carries the opt-in edits.commit query parameters; its zero value
// sends none, so Google's default behavior applies.
type Options struct {
	KeepOnFailure  bool
	ExplicitEditID string
	Commit         CommitOptions
}

// ChangesInReview selects how a commit treats changes that are already in
// Google's review: the changesInReviewBehavior query parameter of edits.commit.
// The values are the CLI spellings; the wire enum is derived in query.
type ChangesInReview string

const (
	// ChangesInReviewUnset sends no parameter, so Google's default applies:
	// CANCEL_IN_REVIEW_AND_SUBMIT, which cancels the pending review and submits
	// everything again. Kept as the default because changing it would change
	// what every existing CI pipeline publishes (#598).
	ChangesInReviewUnset ChangesInReview = ""
	// ChangesInReviewCancel asks for Google's default explicitly.
	ChangesInReviewCancel ChangesInReview = "cancel"
	// ChangesInReviewError makes the commit fail while changes are in review,
	// leaving the review untouched (ERROR_IF_IN_REVIEW). Google does not
	// invalidate the Edit on that refusal.
	ChangesInReviewError ChangesInReview = "error"
)

// ParseChangesInReview validates a CLI spelling. The empty string is the unset
// value, not an error, so a flag left alone maps to Google's default.
func ParseChangesInReview(s string) (ChangesInReview, error) {
	switch v := ChangesInReview(s); v {
	case ChangesInReviewUnset, ChangesInReviewCancel, ChangesInReviewError:
		return v, nil
	}
	return ChangesInReviewUnset, fmt.Errorf("must be %s or %s", ChangesInReviewCancel, ChangesInReviewError)
}

// CommitOptions are the optional query parameters of edits.commit. The zero
// value sends none: the request is byte-identical to the one gplay has always
// sent, which is what keeps these opt-ins additive.
type CommitOptions struct {
	ChangesInReview ChangesInReview
	// ChangesNotSentForReview commits the Edit without sending its changes for
	// review; they wait until someone sends them from the Play Console. Some
	// apps (after a rejection, for instance) cannot commit any other way.
	ChangesNotSentForReview bool
}

// IsZero reports whether o sends no parameter at all.
func (o CommitOptions) IsZero() bool { return o == CommitOptions{} }

// query renders o as the edits.commit query string. Parameter names and enum
// values are Discovery's (androidpublisher.edits.commit).
func (o CommitOptions) query() url.Values {
	q := url.Values{}
	switch o.ChangesInReview {
	case ChangesInReviewCancel:
		q.Set("changesInReviewBehavior", "CANCEL_IN_REVIEW_AND_SUBMIT")
	case ChangesInReviewError:
		q.Set("changesInReviewBehavior", "ERROR_IF_IN_REVIEW")
	}
	if o.ChangesNotSentForReview {
		q.Set("changesNotSentForReview", "true")
	}
	return q
}

// CommitOutcomeUnknownError is an edits.commit that failed in a way that does
// not tell whether Google applied it: the request left the machine and then
// timed out, was reset, or came back 5xx. The commit may be live. Its exit code
// stays the one the wrapped failure maps to (50 or 40), but it is NOT
// retryable (COMMIT_OUTCOME_UNKNOWN): a blind re-run of an upload that did
// publish fails on the already-used version code and reports a successful
// release as an error, and a re-run of a metadata change can resubmit it for
// review. The message says how to check instead.
type CommitOutcomeUnknownError struct {
	Package string
	EditID  string
	// Explicit is set for `gplay edits commit`, whose pin stays in place and
	// whose Edit can therefore be probed with `edits status --live`.
	Explicit bool
	Err      error
}

func (e *CommitOutcomeUnknownError) Error() string {
	check := "check the live state first (for example `gplay releases list --package " + e.Package + "`, or the Play Console)"
	if e.Explicit {
		check = "run `gplay edits status --live --package " + e.Package + "` first (an Edit that is gone was most likely committed) and check the live state before committing again"
	}
	return fmt.Sprintf("commit of edit %s on %s may have been applied before the failure; do not re-run blindly, %s: %v", e.EditID, e.Package, check, e.Err)
}

func (e *CommitOutcomeUnknownError) Unwrap() error { return e.Err }

// ExitCode keeps the wrapped failure's code: the bucket (network, upstream) is
// still true, only its retry-safety is not (#598 keeps the exit code).
func (e *CommitOutcomeUnknownError) ExitCode() int {
	var c interface{ ExitCode() int }
	if errors.As(e.Err, &c) {
		return c.ExitCode()
	}
	return 50
}

// DiagnosticCode refines the envelope's code, which is what flips `retryable`.
func (e *CommitOutcomeUnknownError) DiagnosticCode() exit.Code {
	return exit.CodeCommitOutcomeUnknown
}

// commitOutcomeUnknown wraps err when it leaves the commit's outcome open, and
// returns it unchanged otherwise. Unknown means the request may have reached
// Google's write path: a 5xx, or a transport failure after the connection was
// made. A refused token exchange (the cause carries its own exit code) and a
// dial or DNS failure never sent the commit, the same line the --retry
// transport draws for non-idempotent writes (internal/transport.neverApplied),
// so they keep their plain meaning. A request that failed to build locally
// would also land here as unknown; registry URLs make that unreachable, and
// erring toward "check first" is the safe side.
func commitOutcomeUnknown(err error, pkg, editID string, explicit bool) error {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.StatusCode >= 500:
	case apiErr.StatusCode == 0 && !neverSent(apiErr.Cause):
	default:
		return err
	}
	return &CommitOutcomeUnknownError{Package: pkg, EditID: editID, Explicit: explicit, Err: err}
}

// neverSent reports whether a transport-level cause proves the commit never
// left the machine.
func neverSent(cause error) bool {
	var coder interface{ ExitCode() int }
	if errors.As(cause, &coder) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(cause, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(cause, &opErr) && opErr.Op == "dial"
}

// Discard bounds. The cleanup DELETE never runs on the caller's ctx (a canceled
// or timed-out ctx must not also kill it: a dangling Edit blocks the next
// publish for up to 24h), so it gets its own deadline.
const (
	// discardTimeout leaves room for --retry to replay a 5xx on the DELETE.
	discardTimeout = 10 * time.Second
	// interruptedDiscardTimeout applies once the caller's ctx is done, which
	// in the binary means SIGINT or SIGTERM: a CI runner that cancels a job
	// sends SIGINT, then SIGTERM 7.5s later and SIGKILL 2.5s after that
	// (GitHub Actions), so the discard has to fit well inside the first gap.
	interruptedDiscardTimeout = 5 * time.Second
)

// discardContext returns the fresh context the cleanup DELETE runs on, bounded
// by discardTimeout, or by interruptedDiscardTimeout when parent is already
// done.
func discardContext(parent context.Context) (context.Context, context.CancelFunc) {
	d := discardTimeout
	if parent.Err() != nil {
		d = interruptedDiscardTimeout
	}
	return context.WithTimeout(context.Background(), d)
}

// WithEdit opens an Edit on pkg, invokes fn with the new Edit ID, and
// commits on success. On any failure from fn OR from the final commit,
// the Edit is automatically discarded (edits.delete) before the error
// propagates, unless opts.KeepOnFailure is set, in which case the
// failure is wrapped in a *DanglingEditError carrying the Edit ID so the
// operator can recover. A failed cleanup is intentionally swallowed so
// the caller-supplied error (the real cause) reaches the user.
func WithEdit(ctx context.Context, hc *http.Client, pkg string, opts Options, fn func(editID string) error) error {
	// Explicit mode (`gplay edits begin` has an Edit open and persisted to
	// .gplay/edit-<pkg>.json): run the mutation against the pinned Edit and
	// return. We deliberately do NOT insert, commit, or discard: the user
	// drives those via `gplay edits commit`/`discard`, so a mid-batch failure
	// leaves the Edit open for a retry or an explicit discard.
	if opts.ExplicitEditID != "" {
		if err := fn(opts.ExplicitEditID); err != nil {
			return explainStalePin(ctx, hc, pkg, opts.ExplicitEditID, err)
		}
		return nil
	}
	editID, err := insertEdit(ctx, hc, pkg)
	if err != nil {
		return err
	}
	handleFailure := func(failureErr error) error {
		if !opts.KeepOnFailure {
			// Best-effort discard runs on a fresh, bounded context so a
			// canceled or timed-out parent ctx does not also kill the
			// cleanup: leaving the Edit dangling blocks the user's next
			// publish for up to 24h. `defer cancel()` rather than an
			// inline call guarantees the timer goroutine is released
			// even if deleteEdit panics on a misbehaving transport.
			cleanupCtx, cancel := discardContext(ctx)
			defer cancel()
			_ = deleteEdit(cleanupCtx, hc, pkg, editID)
			return failureErr
		}
		// KeepOnFailure: the caller debugging the failure needs the
		// open Edit ID so they can release it via the Play Console
		// (or wait ~24h for it to expire) once the root cause is fixed.
		return &DanglingEditError{EditID: editID, Err: failureErr}
	}
	// Panic safety: if fn (or any downstream code it calls) panics, we
	// still must clean up the open Edit before letting the panic
	// continue: otherwise a 24h Edit lock leaks. We re-panic after
	// cleanup so the caller observes the original failure unchanged
	// and any higher-level recovery (e.g. test harness, server
	// middleware) still sees it. KeepOnFailure is honored: a debug
	// session that asked to keep the Edit gets the panic with no
	// DELETE side effect (the Edit ID is recoverable via
	// `gplay edits discard`).
	defer func() {
		if r := recover(); r != nil {
			if !opts.KeepOnFailure {
				cleanupCtx, cancel := discardContext(ctx)
				defer cancel()
				_ = deleteEdit(cleanupCtx, hc, pkg, editID)
			}
			panic(r)
		}
	}()

	if fnErr := fn(editID); fnErr != nil {
		return handleFailure(fnErr)
	}
	if commitErr := commitEdit(ctx, hc, pkg, editID, opts.Commit); commitErr != nil {
		// The discard still runs on an unknown outcome: if the commit landed the
		// Edit is gone and the delete is a harmless 404; if it did not, the
		// delete frees the package for the next run.
		return handleFailure(commitOutcomeUnknown(commitErr, pkg, editID, false))
	}
	return nil
}

// WithReadOnlyEdit opens an Edit on pkg, invokes fn with the new Edit ID
// for read-only operations (tracks.get, details.get, ...), and ALWAYS
// discards the Edit afterward: a read-only Edit must never be committed.
// Committing a change-free Edit would, at best, burn one of the daily
// publish slots Google Play rate-limits and, at worst, mutate state; so
// `gplay releases list` / `tracks list` open, read, and discard.
//
// The discard runs in a deferred closure, so it fires on the normal
// return, on a closure error, AND during a panic unwind, after which
// the panic keeps propagating untouched. Unlike WithEdit, no recover()
// is needed: a read-only Edit is ALWAYS discarded, so there is no
// KeepOnFailure branch to honor on the panic path. The cleanup uses a
// fresh bounded context so a canceled or timed-out parent ctx still
// cleans up the open Edit: leaving one dangling blocks the user's next
// publish for up to 24h. fn's error (or nil) propagates verbatim; a
// discard failure is swallowed so the real outcome reaches the caller.
func WithReadOnlyEdit(ctx context.Context, hc *http.Client, pkg string, fn func(editID string) error) error {
	editID, err := insertEdit(ctx, hc, pkg)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := discardContext(ctx)
		defer cancel()
		_ = deleteEdit(cleanupCtx, hc, pkg, editID)
	}()
	return fn(editID)
}

// EditConflictError signals that the upstream insertEdit was rejected
// because an Edit is already open on the package. It is recoverable:
// the open Edit auto-expires after ~24h, or the operator can release
// it via the Google Play Console, so the error message points at those
// remediation paths (the conflicting Edit may have been opened by another
// client, or by a `gplay edits begin` whose pin is in a different repo, so
// `gplay edits discard` is not guaranteed to reach it). The mapping to
// exit 30 (API 4xx, recoverable) rather than the generic state-conflict
// exit 60 stays the same.
type EditConflictError struct {
	Package string
	Err     error
}

func (e *EditConflictError) Error() string {
	return fmt.Sprintf(
		"an Edit is already open on %s (wait ~24h for it to expire, or release it via the Google Play Console): %v",
		e.Package, e.Err,
	)
}

func (e *EditConflictError) Unwrap() error { return e.Err }

// ExitCode forwards to the wrapped *api.Error so the underlying HTTP
// status drives the exit-code mapping: 400+editAlreadyExists stays
// at 30 (API misuse, recoverable), but a 429+editAlreadyExists (Google
// has been known to ship the reason on rate-limited responses) keeps
// its 60 (state conflict, retry-with-backoff) classification rather
// than being silently downgraded. Mirrors the DanglingEditError
// pattern. Falls back to 30 if no Coder is found in the chain.
func (e *EditConflictError) ExitCode() int {
	var c interface{ ExitCode() int }
	if errors.As(e.Err, &c) {
		return c.ExitCode()
	}
	return 30
}

// Validate is a cheap access probe: open an Edit on pkg and immediately
// discard it (no commit). A successful round-trip means the active
// credential has both Play-API authentication AND the per-package
// permission grant: the failure mode `gplay apps add` is most often
// asked to catch.
//
// Validate calls insertEdit + deleteEdit directly (rather than going
// through WithReadOnlyEdit) for one reason: WithReadOnlyEdit
// deliberately swallows the discard error in its `defer` so a
// read-only listing always returns the closure's result. Validate's
// contract is the opposite: the probe MUST report a failed discard,
// because reporting success while leaking a 24h-locked Edit corrupts
// the very state the probe was meant to verify. A failed discard
// surfaces as a *DanglingEditError carrying the open Edit ID.
//
// Insert failures are mapped:
//   - editAlreadyExists reason → *EditConflictError (exit 30 + Play
//     Console recovery hint)
//   - anything else            → the raw *api.Error (exit code via
//     StatusToExitCode: 403→11, 404→30, 429→60, …)
func Validate(ctx context.Context, hc *http.Client, pkg string) error {
	editID, err := insertEdit(ctx, hc, pkg)
	if err != nil {
		if isEditAlreadyExists(err) {
			return &EditConflictError{Package: pkg, Err: err}
		}
		return err
	}
	// The discard runs on a fresh, bounded context so a canceled or
	// timed-out parent ctx does not prevent cleanup. We still propagate
	// any cleanup failure as a DanglingEditError so the operator knows
	// they have an Edit to release manually.
	cleanupCtx, cancel := discardContext(ctx)
	defer cancel()
	if delErr := deleteEdit(cleanupCtx, hc, pkg, editID); delErr != nil {
		return &DanglingEditError{EditID: editID, Err: delErr}
	}
	return nil
}

// OpenExplicit opens a new Edit on pkg and returns its ID WITHOUT committing or
// discarding it: the explicit-mode entrypoint for `gplay edits begin`, which
// persists the returned ID to .gplay/edit-<pkg>.json so later write commands
// reuse it. Google's `editAlreadyExists` (an Edit is already open server-side,
// e.g. one this machine forgot or another client opened) maps to
// *EditConflictError (exit 30 + Play Console recovery hint); any other failure
// surfaces as the raw *api.Error.
func OpenExplicit(ctx context.Context, hc *http.Client, pkg string) (string, error) {
	editID, err := insertEdit(ctx, hc, pkg)
	if err != nil {
		if isEditAlreadyExists(err) {
			return "", &EditConflictError{Package: pkg, Err: err}
		}
		return "", err
	}
	return editID, nil
}

// CommitExplicit commits an already-open Edit (the `gplay edits commit` verb).
// On failure the Edit stays open (no discard), so the operator can re-attempt
// the commit or discard it: the caller leaves .gplay/edit-<pkg>.json in place
// until a commit succeeds. A failure that leaves the outcome open is a
// *CommitOutcomeUnknownError: re-attempting a commit that did land hits a
// vanished Edit.
func CommitExplicit(ctx context.Context, hc *http.Client, pkg, editID string, opts CommitOptions) error {
	return commitOutcomeUnknown(commitEdit(ctx, hc, pkg, editID, opts), pkg, editID, true)
}

// DiscardExplicit discards an already-open Edit (the `gplay edits discard`
// verb). The caller clears .gplay/edit-<pkg>.json regardless of the outcome:
// an Edit that has already expired/vanished server-side is the desired end
// state either way.
func DiscardExplicit(ctx context.Context, hc *http.Client, pkg, editID string) error {
	return deleteEdit(ctx, hc, pkg, editID)
}

// AppEdit is the subset of the API's AppEdit resource the explicit lifecycle
// reads back: the id and the expiry (Unix seconds, a string in the JSON).
type AppEdit struct {
	ID                string `json:"id"`
	ExpiryTimeSeconds string `json:"expiryTimeSeconds"`
}

// ValidateExplicit validates an already-open Edit without committing it (the
// `gplay edits validate` verb, edits.validate): Google runs the checks a commit
// would run and rejects with its usual error envelope, surfaced as *api.Error.
// The Edit stays open whatever the outcome. The raw 2xx body (an AppEdit) is
// returned so the command can mirror it verbatim under --output json
// (ADR-0003).
func ValidateExplicit(ctx context.Context, hc *http.Client, pkg, editID string) (json.RawMessage, error) {
	return callEdit(ctx, hc, "edits.validate", methodValidate, pkg, editID)
}

// GetExplicit reads an Edit server-side (edits.get): the `gplay edits status
// --live` probe. An Edit that expired or was discarded by another client comes
// back as a 404, surfaced as the raw *api.Error so the caller can tell "gone"
// apart from any other failure via StatusCode.
func GetExplicit(ctx context.Context, hc *http.Client, pkg, editID string) (AppEdit, json.RawMessage, error) {
	raw, err := callEdit(ctx, hc, "edits.get", methodGet, pkg, editID)
	if err != nil {
		return AppEdit{}, nil, err
	}
	var parsed AppEdit
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return AppEdit{}, nil, &api.Error{Operation: "edits.get", Package: pkg, StatusCode: http.StatusOK, Message: "decode response: " + err.Error(), Cause: err}
	}
	return parsed, raw, nil
}

// callEdit issues a body-less request on an {packageName, editId}-addressed
// method and returns the 2xx body, mapping a non-2xx to *api.Error exactly
// like the insert/commit/delete helpers above.
func callEdit(ctx context.Context, hc *http.Client, op string, m apiregistry.Method, pkg, editID string) (json.RawMessage, error) {
	u, err := m.URL(map[string]string{"packageName": pkg, "editId": editID})
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, m.Verb, u, http.NoBody)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "read response: " + err.Error(), Cause: err}
	}
	return body, nil
}

// isEditAlreadyExists reports whether err carries Google Play's
// `editAlreadyExists` reason. The envelope's top-level message is
// often a generic "Edit ID is required" string; the discriminating
// signal lives in error.errors[].reason and is preserved on
// api.Error.Reasons.
func isEditAlreadyExists(err error) bool {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return hasReason(apiErr, "editAlreadyExists")
}

// hasReason reports whether e carries the Google error.errors[].reason want,
// compared case-insensitively like the exit classifier does.
func hasReason(e *api.Error, want string) bool {
	for _, r := range e.Reasons {
		if strings.EqualFold(strings.TrimSpace(r), want) {
			return true
		}
	}
	return false
}

func insertEdit(ctx context.Context, hc *http.Client, pkg string) (string, error) {
	u, err := methodInsert.URL(map[string]string{"packageName": pkg})
	if err != nil {
		return "", &api.Error{Operation: "edits.insert", Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, methodInsert.Verb, u, http.NoBody)
	if err != nil {
		return "", &api.Error{Operation: "edits.insert", Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", &api.Error{Operation: "edits.insert", Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return "", &api.Error{
			Operation:  "edits.insert",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", &api.Error{
			Operation:  "edits.insert",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	if parsed.ID == "" {
		return "", &api.Error{
			Operation:  "edits.insert",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "empty Edit ID in response body",
		}
	}
	return parsed.ID, nil
}

// deleteEdit best-effort discards an open Edit. Errors are returned for
// telemetry but the caller (WithEdit) treats them as non-fatal so the
// real upstream error is not masked.
func deleteEdit(ctx context.Context, hc *http.Client, pkg, editID string) error {
	u, err := methodDelete.URL(map[string]string{"packageName": pkg, "editId": editID})
	if err != nil {
		return &api.Error{Operation: "edits.delete", Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, methodDelete.Verb, u, http.NoBody)
	if err != nil {
		return &api.Error{Operation: "edits.delete", Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &api.Error{Operation: "edits.delete", Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return &api.Error{
			Operation:  "edits.delete",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	return nil
}

// commitEdit sends edits.commit through the executor. A 2xx is success
// whatever happens to its body: the AppEdit it carries is never used, and a
// response cut mid-body must not report a live publish as a failure.
func commitEdit(ctx context.Context, hc *http.Client, pkg, editID string, opts CommitOptions) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: methodCommit,
		Params: map[string]string{"packageName": pkg, "editId": editID},
		Query:  opts.query(),
		Target: pkg,
	})
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 200 && apiErr.StatusCode < 300 {
		return nil
	}
	return err
}
