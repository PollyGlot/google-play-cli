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
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// Options tunes the Edit lifecycle. KeepOnFailure suppresses the
// auto-discard cleanup when the closure returns an error — see Block 2.
type Options struct {
	KeepOnFailure bool
}

// WithEdit opens an Edit on pkg, invokes fn with the new Edit ID, and
// commits on success. On any failure from fn, the Edit is automatically
// discarded (edits.delete) before the error propagates — unless
// opts.KeepOnFailure is set. A failed cleanup is intentionally swallowed
// so the caller-supplied error (the real cause) reaches the user.
func WithEdit(ctx context.Context, hc *http.Client, pkg string, opts Options, fn func(editID string) error) error {
	editID, err := insertEdit(ctx, hc, pkg)
	if err != nil {
		return err
	}
	if fnErr := fn(editID); fnErr != nil {
		if !opts.KeepOnFailure {
			_ = deleteEdit(ctx, hc, pkg, editID)
		}
		return fnErr
	}
	return commitEdit(ctx, hc, pkg, editID)
}

func insertEdit(ctx context.Context, hc *http.Client, pkg string) (string, error) {
	u := api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) + "/edits"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, http.NoBody)
	if err != nil {
		return "", &api.Error{Operation: "edits.insert", Package: pkg, Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", &api.Error{Operation: "edits.insert", Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIBodyRead))
		return "", &api.Error{
			Operation:  "edits.insert",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    api.APIErrorMessage(body, resp.StatusCode),
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIBodyRead))
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", &api.Error{
			Operation:  "edits.insert",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
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
		return &api.Error{Operation: "edits.delete", Package: pkg, Message: err.Error()}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &api.Error{Operation: "edits.delete", Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIBodyRead))
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
		return &api.Error{Operation: "edits.commit", Package: pkg, Message: err.Error()}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &api.Error{Operation: "edits.commit", Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIBodyRead))
		return &api.Error{
			Operation:  "edits.commit",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    api.APIErrorMessage(body, resp.StatusCode),
		}
	}
	return nil
}
