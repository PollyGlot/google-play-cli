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
	"net/http"
	"strings"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// Registry entries this package calls. Resolving them at init makes an
// unregistered or vanished method a startup panic caught by CI (the registry
// tests resolve every entry) rather than a runtime surprise; verb and URL then
// come from the Discovery snapshot instead of literals kept here (#513).
var (
	methodInsert = apiregistry.MustResolve("androidpublisher.edits.insert")
	methodDelete = apiregistry.MustResolve("androidpublisher.edits.delete")
	methodCommit = apiregistry.MustResolve("androidpublisher.edits.commit")
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

// Options tunes the Edit lifecycle. KeepOnFailure suppresses the
// auto-discard cleanup when the closure returns an error: see Block 2.
//
// ExplicitEditID selects the explicit-mode (`gplay edits begin/commit/discard`)
// contract: when it is non-empty, WithEdit runs fn against that already-open
// Edit WITHOUT opening, committing, or discarding one: the caller owns the
// lifecycle. KeepOnFailure is moot in that mode (nothing is auto-discarded).
type Options struct {
	KeepOnFailure  bool
	ExplicitEditID string
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
		return fn(opts.ExplicitEditID)
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
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = deleteEdit(cleanupCtx, hc, pkg, editID)
			}
			panic(r)
		}
	}()

	if fnErr := fn(editID); fnErr != nil {
		return handleFailure(fnErr)
	}
	if commitErr := commitEdit(ctx, hc, pkg, editID); commitErr != nil {
		return handleFailure(commitErr)
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
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
// until a commit succeeds.
func CommitExplicit(ctx context.Context, hc *http.Client, pkg, editID string) error {
	return commitEdit(ctx, hc, pkg, editID)
}

// DiscardExplicit discards an already-open Edit (the `gplay edits discard`
// verb). The caller clears .gplay/edit-<pkg>.json regardless of the outcome:
// an Edit that has already expired/vanished server-side is the desired end
// state either way.
func DiscardExplicit(ctx context.Context, hc *http.Client, pkg, editID string) error {
	return deleteEdit(ctx, hc, pkg, editID)
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
	for _, r := range apiErr.Reasons {
		if strings.EqualFold(r, "editAlreadyExists") {
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

func commitEdit(ctx context.Context, hc *http.Client, pkg, editID string) error {
	u, err := methodCommit.URL(map[string]string{"packageName": pkg, "editId": editID})
	if err != nil {
		return &api.Error{Operation: "edits.commit", Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, methodCommit.Verb, u, http.NoBody)
	if err != nil {
		return &api.Error{Operation: "edits.commit", Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &api.Error{Operation: "edits.commit", Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return &api.Error{
			Operation:  "edits.commit",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	return nil
}
