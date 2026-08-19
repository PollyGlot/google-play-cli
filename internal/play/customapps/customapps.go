// Package customapps creates a private, organisation-scoped app through managed
// Google Play via playcustomapp.accounts.customApps.create: the one Developer
// API path that creates an app *record* (public apps are Console-only). It is
// keyed by the developer-account axis (accounts/{account}, ADR-0015), not a
// package: the app does not yet exist to be keyed by package. The call is a
// resumable media upload (ADR-0007, raw HTTP; PRD #355): the JSON CustomApp
// metadata opens the resumable session in the initiate POST and the AAB/APK
// artifact streams in chunked PUTs, resuming from a server-acknowledged offset
// on a transient failure.
//
// The whole upstream surface is this one method: there is NO get/list (no
// read) and NO delete, which is why creation is gated behind --confirm at the
// command layer (irreversible; ADR-0032). Every upstream failure surfaces as an
// *api.Error so the gplay exit-code taxonomy maps transparently (403→11,
// 5xx→40, network→50); a missing/unreadable artifact is a client-side
// *LocalIOError (exit 20).
package customapps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const op = "customApps.create"

// Organization is an org the custom app is restricted to. JSON tags mirror the
// API verbatim for the ADR-0003 pass-through.
type Organization struct {
	OrganizationID   string `json:"organizationId,omitempty"`
	OrganizationName string `json:"organizationName,omitempty"`
}

// CustomApp is the API-shaped request/response. packageName is output-only
// (present only in the response).
type CustomApp struct {
	Title         string         `json:"title,omitempty"`
	LanguageCode  string         `json:"languageCode,omitempty"`
	PackageName   string         `json:"packageName,omitempty"`
	Organizations []Organization `json:"organizations,omitempty"`
}

// CreateOpts is the create-request metadata (everything but the artifact).
type CreateOpts struct {
	Title         string
	LanguageCode  string
	Organizations []Organization
}

// LocalIOError is returned when the artifact cannot be read from the local
// filesystem (missing path, permission denied, stat failure, non-regular
// file). It is distinct from *api.Error so the exit code maps to client-side
// validation (20 per docs/DESIGN.md §9) rather than transport (50): parity
// with bundles.upload.
type LocalIOError struct {
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: %s: %v", op, e.Path, e.Cause)
}

func (e *LocalIOError) Unwrap() error { return e.Cause }

// ExitCode satisfies gplay's Coder contract: a missing or unreadable artifact
// is a client-side validation failure, not a transport problem.
func (e *LocalIOError) ExitCode() int { return 20 }

// Create uploads artifactPath plus the CustomApp metadata to
// customApps.create and returns the created CustomApp (carrying its
// output-only packageName) plus the raw JSON response (ADR-0003). account is
// the developer-account ID: the accounts/{account} path key.
//
// The transfer is a resumable upload (PRD #355): the CustomApp metadata JSON
// opens the session in the initiate POST (uploadType=resumable) and the
// artifact streams in the chunk PUTs. The surface is unchanged: the resource
// body Google returns on the final chunk is the same CustomApp payload the
// former multipart upload returned.
func Create(ctx context.Context, hc *http.Client, account, artifactPath string, opts CreateOpts) (CustomApp, json.RawMessage, error) {
	metaJSON, err := json.Marshal(CustomApp{
		Title:         opts.Title,
		LanguageCode:  opts.LanguageCode,
		Organizations: opts.Organizations,
	})
	if err != nil {
		return CustomApp{}, nil, &api.Error{Operation: op, Package: account, Message: "marshal metadata: " + err.Error(), Cause: err}
	}

	f, err := os.Open(artifactPath)
	if err != nil {
		return CustomApp{}, nil, &LocalIOError{Path: artifactPath, Cause: err}
	}
	defer func() { _ = f.Close() }()
	// Stat the artifact for its size (the resumable helper needs the exact
	// byte count for X-Upload-Content-Length and the chunk Content-Range
	// headers). A directory / fifo passes Open+Stat but cannot be streamed, so
	// reject anything but a regular file up front: parity with bundles.upload.
	info, err := f.Stat()
	if err != nil {
		return CustomApp{}, nil, &LocalIOError{Path: artifactPath, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return CustomApp{}, nil, &LocalIOError{Path: artifactPath, Cause: fmt.Errorf("not a regular file")}
	}

	u := api.CustomAppUploadBase + "/accounts/" + url.PathEscape(account) + "/customApps?uploadType=resumable"

	// *os.File is an io.ReaderAt, giving the resumable helper random access to
	// re-send from a server-acknowledged offset after a transient failure
	// without reopening the file. The metadata JSON travels in the initiate
	// body; the artifact travels in the chunk PUTs (application/octet-stream).
	raw, status, err := api.ResumableUploadWithInitiateBody(
		ctx, hc, op, account, u, "application/octet-stream", f, info.Size(),
		metaJSON, "application/json; charset=UTF-8",
	)
	if err != nil {
		return CustomApp{}, nil, err
	}

	var parsed CustomApp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return CustomApp{}, nil, &api.Error{Operation: op, Package: account, StatusCode: status, Message: "decode response: " + err.Error(), Cause: err}
	}
	return parsed, raw, nil
}
