// Package customapps creates a private, organisation-scoped app through managed
// Google Play via playcustomapp.accounts.customApps.create — the one Developer
// API path that creates an app *record* (public apps are Console-only). It is
// keyed by the developer-account axis (accounts/{account}, ADR-0015), not a
// package: the app does not yet exist to be keyed by package. The call is a
// hand-rolled multipart/related media upload (ADR-0007, raw HTTP): a JSON
// CustomApp metadata part plus the AAB/APK artifact in a single POST.
//
// The whole upstream surface is this one method — there is NO get/list (no
// read) and NO delete, which is why creation is gated behind --confirm at the
// command layer (irreversible; ADR-0032). Every upstream failure surfaces as an
// *api.Error so the gplay exit-code taxonomy maps transparently (403→11,
// 5xx→40, network→50); a missing/unreadable artifact is a client-side
// *LocalIOError (exit 20).
package customapps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
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
// validation (20 per docs/DESIGN.md §9) rather than transport (50) — parity
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

// multiReadCloser adapts an io.Reader + the underlying io.Closer into a single
// ReadCloser, so a GetBody replay closes the freshly-opened artifact file
// rather than leaking its descriptor.
type multiReadCloser struct {
	io.Reader
	io.Closer
}

// Create uploads artifactPath plus the CustomApp metadata to
// customApps.create and returns the created CustomApp (carrying its
// output-only packageName) plus the raw JSON response (ADR-0003). account is
// the developer-account ID — the accounts/{account} path key.
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
	// Stat the artifact so we can set ContentLength explicitly (no chunked
	// Transfer-Encoding, which Google's upload front-ends handle poorly for
	// large bodies and which blocks transport retries — same rationale as
	// bundles.upload). A directory / fifo passes Open+Stat but cannot be
	// streamed as a body, so reject anything but a regular file up front.
	info, err := f.Stat()
	if err != nil {
		return CustomApp{}, nil, &LocalIOError{Path: artifactPath, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return CustomApp{}, nil, &LocalIOError{Path: artifactPath, Cause: fmt.Errorf("not a regular file")}
	}

	// Build the multipart/related head (the JSON metadata part plus the media
	// part's headers) and tail (the closing boundary) up front, so the artifact
	// streams BETWEEN them with an exact ContentLength and a replayable GetBody
	// — the body is never buffered in memory (artifacts run to 10 GiB).
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	jpart, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}})
	if err != nil {
		return CustomApp{}, nil, &api.Error{Operation: op, Package: account, Message: err.Error(), Cause: err}
	}
	if _, err := jpart.Write(metaJSON); err != nil {
		return CustomApp{}, nil, &api.Error{Operation: op, Package: account, Message: err.Error(), Cause: err}
	}
	// Open the media part's headers but write none of its body here; the
	// artifact bytes are streamed in separately below.
	if _, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/octet-stream"}}); err != nil {
		return CustomApp{}, nil, &api.Error{Operation: op, Package: account, Message: err.Error(), Cause: err}
	}
	boundary := mw.Boundary()
	headBytes := head.Bytes()
	// NOT mw.Close(): that would append the closing boundary to head BEFORE the
	// artifact bytes. Build the tail (what Close would emit) by hand instead.
	tail := []byte("\r\n--" + boundary + "--\r\n")

	newBody := func(media io.Reader) io.Reader {
		return io.MultiReader(bytes.NewReader(headBytes), media, bytes.NewReader(tail))
	}

	u := api.CustomAppUploadBase + "/accounts/" + url.PathEscape(account) + "/customApps?uploadType=multipart"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, newBody(f))
	if err != nil {
		return CustomApp{}, nil, &api.Error{Operation: op, Package: account, Message: err.Error(), Cause: err}
	}
	req.ContentLength = int64(len(headBytes)) + info.Size() + int64(len(tail))
	req.Header.Set("Content-Type", "multipart/related; boundary="+boundary)
	// GetBody re-opens the artifact so the transport can replay the body across
	// a 3xx redirect or an oauth2 token-refresh retry — the closer is the fresh
	// file, so each replay frees its own descriptor (parity with bundles.go).
	req.GetBody = func() (io.ReadCloser, error) {
		rf, err := os.Open(artifactPath)
		if err != nil {
			return nil, err
		}
		return multiReadCloser{Reader: newBody(rf), Closer: rf}, nil
	}

	resp, err := hc.Do(req)
	if err != nil {
		return CustomApp{}, nil, &api.Error{Operation: op, Package: account, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return CustomApp{}, nil, &api.Error{
			Operation:  op,
			Package:    account,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if err != nil {
		return CustomApp{}, nil, &api.Error{Operation: op, Package: account, StatusCode: resp.StatusCode, Message: "read response body: " + err.Error(), Cause: err}
	}
	var parsed CustomApp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return CustomApp{}, nil, &api.Error{Operation: op, Package: account, StatusCode: resp.StatusCode, Message: "decode response: " + err.Error(), Cause: err}
	}
	return parsed, raw, nil
}
