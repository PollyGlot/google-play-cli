package edits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

var (
	methodInsert   = apiregistry.MustResolve("androidpublisher.edits.insert")
	methodDelete   = apiregistry.MustResolve("androidpublisher.edits.delete")
	methodCommit   = apiregistry.MustResolve("androidpublisher.edits.commit")
	methodGet      = apiregistry.MustResolve("androidpublisher.edits.get")
	methodValidate = apiregistry.MustResolve("androidpublisher.edits.validate")
)

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

func (e *StalePinError) ExitCode() int {
	var c interface{ ExitCode() int }
	if errors.As(e.Err, &c) {
		return c.ExitCode()
	}
	return 30
}

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

type Options struct {
	KeepOnFailure  bool
	ExplicitEditID string
	Commit         CommitOptions
}

type ChangesInReview string

const (
	ChangesInReviewUnset  ChangesInReview = ""
	ChangesInReviewCancel ChangesInReview = "cancel"
	ChangesInReviewError  ChangesInReview = "error"
)

func ParseChangesInReview(s string) (ChangesInReview, error) {
	switch v := ChangesInReview(s); v {
	case ChangesInReviewUnset, ChangesInReviewCancel, ChangesInReviewError:
		return v, nil
	}
	return ChangesInReviewUnset, fmt.Errorf("must be %s or %s", ChangesInReviewCancel, ChangesInReviewError)
}

func (c ChangesInReview) Wire() string {
	switch c {
	case ChangesInReviewCancel:
		return "CANCEL_IN_REVIEW_AND_SUBMIT"
	case ChangesInReviewError:
		return "ERROR_IF_IN_REVIEW"
	default:
		return ""
	}
}

type CommitOptions struct {
	ChangesInReview         ChangesInReview
	ChangesNotSentForReview bool
}

func (o CommitOptions) IsZero() bool { return o == CommitOptions{} }

func (o CommitOptions) query() url.Values {
	q := url.Values{}
	if v := o.ChangesInReview.Wire(); v != "" {
		q.Set("changesInReviewBehavior", v)
	}
	if o.ChangesNotSentForReview {
		q.Set("changesNotSentForReview", "true")
	}
	return q
}

type CommitOutcomeUnknownError struct {
	Package  string
	EditID   string
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

func (e *CommitOutcomeUnknownError) ExitCode() int {
	var c interface{ ExitCode() int }
	if errors.As(e.Err, &c) {
		return c.ExitCode()
	}
	return 50
}

func (e *CommitOutcomeUnknownError) DiagnosticCode() exit.Code {
	return exit.CodeCommitOutcomeUnknown
}

func commitOutcomeUnknown(err error, pkg, editID string, explicit bool) error {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.StatusCode >= 500:
	case apiErr.StatusCode == 0 && !api.NeverSent(apiErr.Cause):
	default:
		return err
	}
	return &CommitOutcomeUnknownError{Package: pkg, EditID: editID, Explicit: explicit, Err: err}
}

const (
	discardTimeout            = 10 * time.Second
	interruptedDiscardTimeout = 5 * time.Second
)

func discardContext(parent context.Context) (context.Context, context.CancelFunc) {
	d := discardTimeout
	if parent.Err() != nil {
		d = interruptedDiscardTimeout
	}
	return context.WithTimeout(context.Background(), d)
}

var ErrNothingToCommit = errors.New("edits: nothing to commit")

func WithEdit(ctx context.Context, hc *http.Client, pkg string, opts Options, fn func(editID string) error) error {
	if opts.ExplicitEditID != "" {
		if err := fn(opts.ExplicitEditID); err != nil {
			if errors.Is(err, ErrNothingToCommit) {
				return nil
			}
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
			cleanupCtx, cancel := discardContext(ctx)
			defer cancel()
			_ = deleteEdit(cleanupCtx, hc, pkg, editID)
			return failureErr
		}
		return &DanglingEditError{EditID: editID, Err: failureErr}
	}
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
		if errors.Is(fnErr, ErrNothingToCommit) {
			cleanupCtx, cancel := discardContext(ctx)
			defer cancel()
			if delErr := deleteEdit(cleanupCtx, hc, pkg, editID); delErr != nil {
				return &DanglingEditError{EditID: editID, Err: delErr}
			}
			return nil
		}
		return handleFailure(fnErr)
	}
	if commitErr := commitEdit(ctx, hc, pkg, editID, opts.Commit); commitErr != nil {
		return handleFailure(commitOutcomeUnknown(commitErr, pkg, editID, false))
	}
	return nil
}

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

func (e *EditConflictError) ExitCode() int {
	var c interface{ ExitCode() int }
	if errors.As(e.Err, &c) {
		return c.ExitCode()
	}
	return 30
}

func Validate(ctx context.Context, hc *http.Client, pkg string) error {
	editID, err := insertEdit(ctx, hc, pkg)
	if err != nil {
		if isEditAlreadyExists(err) {
			return &EditConflictError{Package: pkg, Err: err}
		}
		return err
	}
	cleanupCtx, cancel := discardContext(ctx)
	defer cancel()
	if delErr := deleteEdit(cleanupCtx, hc, pkg, editID); delErr != nil {
		return &DanglingEditError{EditID: editID, Err: delErr}
	}
	return nil
}

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

func CommitExplicit(ctx context.Context, hc *http.Client, pkg, editID string, opts CommitOptions) error {
	return commitOutcomeUnknown(commitEdit(ctx, hc, pkg, editID, opts), pkg, editID, true)
}

func DiscardExplicit(ctx context.Context, hc *http.Client, pkg, editID string) error {
	return deleteEdit(ctx, hc, pkg, editID)
}

type AppEdit struct {
	ID                string `json:"id"`
	ExpiryTimeSeconds string `json:"expiryTimeSeconds"`
}

func ValidateExplicit(ctx context.Context, hc *http.Client, pkg, editID string) (json.RawMessage, error) {
	var parsed AppEdit
	return callEdit(ctx, hc, "edits.validate", methodValidate, pkg, editID, &parsed)
}

func GetExplicit(ctx context.Context, hc *http.Client, pkg, editID string) (AppEdit, json.RawMessage, error) {
	var parsed AppEdit
	raw, err := callEdit(ctx, hc, "edits.get", methodGet, pkg, editID, &parsed)
	if err != nil {
		return AppEdit{}, nil, err
	}
	return parsed, raw, nil
}

func callEdit(ctx context.Context, hc *http.Client, op string, m apiregistry.Method, pkg, editID string, out *AppEdit) (json.RawMessage, error) {
	return api.DoJSON(ctx, hc, api.Call{
		Method: m, Op: op, Target: pkg,
		Params: map[string]string{"packageName": pkg, "editId": editID},
	}, out)
}

func isEditAlreadyExists(err error) bool {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return hasReason(apiErr, "editAlreadyExists")
}

func hasReason(e *api.Error, want string) bool {
	for _, r := range e.Reasons {
		if strings.EqualFold(strings.TrimSpace(r), want) {
			return true
		}
	}
	return false
}

func insertEdit(ctx context.Context, hc *http.Client, pkg string) (string, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodInsert, Op: "edits.insert", Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Body:   []byte{},
	})
	if err != nil {
		return "", err
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", api.DecodeFailure(api.Error{
			Operation: "edits.insert",
			Package:   pkg,
			Message:   "decode response: " + err.Error(),
			Cause:     err,
		})
	}
	if parsed.ID == "" {
		return "", api.DecodeFailure(api.Error{
			Operation: "edits.insert",
			Package:   pkg,
			Message:   "empty Edit ID in response body",
		})
	}
	return parsed.ID, nil
}

func deleteEdit(ctx context.Context, hc *http.Client, pkg, editID string) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: methodDelete, Op: "edits.delete", Target: pkg,
		Params: map[string]string{"packageName": pkg, "editId": editID},
	})
	return err
}

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
