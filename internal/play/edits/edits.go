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
	"net/url"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// DanglingEditError wraps the upstream failure that caused an Edit to be
// left open in KeepOnFailure mode. It carries the Edit ID so the
// operator can recover via `gplay edits discard --package <P>` or
// `gplay auth doctor --package <P>` once the issue is fixed. It
// implements gplay's Coder contract by inheriting from the wrapped
// error when possible, falling back to exit 60 (state conflict) so the
// dangling Edit surfaces as a retryable state condition.
type DanglingEditError struct {
	EditID string
	Err    error
}

func (e *DanglingEditError) Error() string {
	return fmt.Sprintf("edit %s left open (run `gplay edits discard --package <pkg>` to clean up): %v", e.EditID, e.Err)
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
// auto-discard cleanup when the closure returns an error — see Block 2.
type Options struct {
	KeepOnFailure bool
}

// WithEdit opens an Edit on pkg, invokes fn with the new Edit ID, and
// commits on success. On any failure from fn OR from the final commit,
// the Edit is automatically discarded (edits.delete) before the error
// propagates — unless opts.KeepOnFailure is set, in which case the
// failure is wrapped in a *DanglingEditError carrying the Edit ID so the
// operator can recover. A failed cleanup is intentionally swallowed so
// the caller-supplied error (the real cause) reaches the user.
func WithEdit(ctx context.Context, hc *http.Client, pkg string, opts Options, fn func(editID string) error) error {
	editID, err := insertEdit(ctx, hc, pkg)
	if err != nil {
		return err
	}
	handleFailure := func(failureErr error) error {
		if !opts.KeepOnFailure {
			// Best-effort discard runs on a fresh, bounded context so a
			// canceled or timed-out parent ctx does not also kill the
			// cleanup — leaving the Edit dangling blocks the user's next
			// publish for up to 24h.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = deleteEdit(cleanupCtx, hc, pkg, editID)
			cancel()
			return failureErr
		}
		// KeepOnFailure: the caller debugging the failure needs the
		// open Edit ID so they can `gplay edits discard` it manually.
		return &DanglingEditError{EditID: editID, Err: failureErr}
	}
	// Panic safety: if fn (or any downstream code it calls) panics, we
	// still must clean up the open Edit before letting the panic
	// continue — otherwise a 24h Edit lock leaks. We re-panic after
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
				_ = deleteEdit(cleanupCtx, hc, pkg, editID)
				cancel()
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

func insertEdit(ctx context.Context, hc *http.Client, pkg string) (string, error) {
	u := api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) + "/edits"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, http.NoBody)
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
		return "", &api.Error{
			Operation:  "edits.insert",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    api.APIErrorMessage(body, resp.StatusCode),
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
	u := api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) + "/edits/" + url.PathEscape(editID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, http.NoBody)
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
		return &api.Error{
			Operation:  "edits.delete",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    api.APIErrorMessage(body, resp.StatusCode),
		}
	}
	return nil
}

func commitEdit(ctx context.Context, hc *http.Client, pkg, editID string) error {
	u := api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) + "/edits/" + url.PathEscape(editID) + ":commit"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, http.NoBody)
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
		return &api.Error{
			Operation:  "edits.commit",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    api.APIErrorMessage(body, resp.StatusCode),
		}
	}
	return nil
}
