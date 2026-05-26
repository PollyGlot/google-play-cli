// Package details reads app-level metadata. Two entry points coexist:
//
//   - GetDefaultLanguage is the low-level helper used inside an already-
//     open Edit (internal/releases/notes resolves the locale for a single
//     --release-notes text). It does one GET on edits.details.get and
//     returns just the defaultLanguage string.
//
//   - Get is the high-level entry point used by `gplay apps info`. It
//     opens a read-only Edit on its own, fetches details.get +
//     listings.get(defaultLanguage), and discards the Edit. Returns the
//     typed *Details plus the raw JSON envelope
//     {"details":..,"listing":..} (explicit exception to ADR-0003
//     because two endpoints are merged).
package details

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

const (
	opDetailsGet  = "details.get"
	opListingsGet = "listings.get"
)

// Details surfaces the three fields `gplay apps info` displays — the
// minimum needed to confirm "yes, I'm looking at the right app".
// defaultLanguage and contactEmail come from edits.details.get;
// title comes from edits.listings.get on the default language.
type Details struct {
	DefaultLanguage string `json:"defaultLanguage"`
	Title           string `json:"title"`
	ContactEmail    string `json:"contactEmail"`
}

// Get opens a read-only Edit on pkg, reads details.get +
// listings.get(defaultLanguage), discards the Edit, and returns both
// the typed *Details and the raw JSON envelope. The envelope shape is
// {"details": <details.get verbatim>, "listing": <listings.get verbatim>}
// — an explicit exception to ADR-0003 documented in the ADR's
// Exceptions section, because apps info combines two endpoints.
//
// Errors propagate as *api.Error so the gplay exit-code taxonomy maps
// transparently: 403 → 11, 404 → 30, 5xx → 40, network → 50. The Edit
// is always discarded (WithReadOnlyEdit's deferred cleanup) even on
// failure — a dangling Edit would block the user's next publish for ~24h.
func Get(ctx context.Context, hc *http.Client, pkg string) (*Details, json.RawMessage, error) {
	var (
		out *Details
		raw json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(ctx, hc, pkg, func(editID string) error {
		detailsRaw, defaultLang, contactEmail, err := fetchDetails(ctx, hc, pkg, editID)
		if err != nil {
			return err
		}
		listingRaw, title, err := fetchListing(ctx, hc, pkg, editID, defaultLang)
		if err != nil {
			return err
		}
		envelope, err := json.Marshal(struct {
			Details json.RawMessage `json:"details"`
			Listing json.RawMessage `json:"listing"`
		}{
			Details: detailsRaw,
			Listing: listingRaw,
		})
		if err != nil {
			// Operation names elsewhere in internal/play/* match a real
			// upstream endpoint (edits.insert, tracks.get, …) so log
			// readers can correlate them with the Google Play API
			// reference. The envelope marshal happens locally and has
			// no upstream peer; use a "details." prefix so a reader
			// sees "this came from the details package, not from a
			// listings.get response we mishandled". Defensive — both
			// inputs are json.RawMessage and can't actually fail
			// json.Marshal — but if the struct ever sprouts a typed
			// field, the tag stays in-domain.
			return &api.Error{Operation: "details.envelope", Package: pkg, Message: "marshal envelope: " + err.Error(), Cause: err}
		}
		out = &Details{
			DefaultLanguage: defaultLang,
			Title:           title,
			ContactEmail:    contactEmail,
		}
		raw = envelope
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return out, raw, nil
}

// fetchDetails GETs edits.details.get and returns (raw body,
// defaultLanguage, contactEmail, err). The raw body is what feeds the
// `"details"` slot of the apps-info envelope.
func fetchDetails(ctx context.Context, hc *http.Client, pkg, editID string) (json.RawMessage, string, string, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/details"
	raw, status, err := getJSON(ctx, hc, opDetailsGet, pkg, u)
	if err != nil {
		return nil, "", "", err
	}
	var parsed struct {
		DefaultLanguage string `json:"defaultLanguage"`
		ContactEmail    string `json:"contactEmail"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, "", "", &api.Error{
			Operation:  opDetailsGet,
			Package:    pkg,
			StatusCode: status,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	if parsed.DefaultLanguage == "" {
		// Mirror GetDefaultLanguage's invariant (details.go:217): an
		// empty defaultLanguage with a 2xx response is an API contract
		// violation. Without this guard, fetchListing would be called
		// with language="", producing a malformed /listings/ URL and a
		// confusing downstream 404 that hides the root cause.
		return nil, "", "", &api.Error{
			Operation:  opDetailsGet,
			Package:    pkg,
			StatusCode: status,
			Message:    "missing defaultLanguage in response body",
		}
	}
	return raw, parsed.DefaultLanguage, parsed.ContactEmail, nil
}

// fetchListing GETs edits.listings.get on a single locale, returning
// (raw body, title, err). title alone is the only field gplay surfaces
// today; the rest of the listing is preserved in the raw envelope for
// --output json consumers who want richer detail.
func fetchListing(ctx context.Context, hc *http.Client, pkg, editID, language string) (json.RawMessage, string, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/listings/" + url.PathEscape(language)
	raw, status, err := getJSON(ctx, hc, opListingsGet, pkg, u)
	if err != nil {
		return nil, "", err
	}
	var parsed struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, "", &api.Error{
			Operation:  opListingsGet,
			Package:    pkg,
			StatusCode: status,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return raw, parsed.Title, nil
}

// getJSON is the shared scaffolding for a GET that returns a JSON body
// inside an open Edit: build the request, run it, on 2xx return the raw
// bytes (capped at MaxAPISuccessBodyRead) AND the HTTP status, on non-2xx
// wrap the error envelope in an *api.Error tagged with op so the exit-code
// taxonomy stays uniform across details.get and listings.get. Returning
// the HTTP status alongside the body lets callers stamp a downstream
// decode-failure *api.Error with the real status (200 in practice, but
// 204 or 206 are valid 2xx the server might emit) rather than hardcoding
// it — keeping parity with GetDefaultLanguage's behavior.
func getJSON(ctx context.Context, hc *http.Client, op, pkg, u string) (json.RawMessage, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, 0, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// A truncated/interrupted read on the error path would
		// otherwise mask the real reason behind a generic
		// ParseErrorEnvelope fallback ("HTTP <status>"). Surface the
		// read failure verbatim so the operator knows the request
		// reached the server but the response stream broke — a
		// retryable network condition mapped to exit 50 by
		// StatusToExitCode when StatusCode is 0, or to the HTTP class
		// otherwise.
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		if readErr != nil {
			return nil, resp.StatusCode, &api.Error{
				Operation:  op,
				Package:    pkg,
				StatusCode: resp.StatusCode,
				Message:    "read error response body: " + readErr.Error(),
				Cause:      readErr,
			}
		}
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, resp.StatusCode, &api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	// Same defensive read on the success path: a partial JSON body
	// would otherwise reach json.Unmarshal and surface as a
	// "decode response" error that buries the real (network) cause.
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, resp.StatusCode, &api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "read response body: " + readErr.Error(),
			Cause:      readErr,
		}
	}
	return raw, resp.StatusCode, nil
}

// GetDefaultLanguage returns the app's defaultLanguage from
// edits.details.get. Errors are wrapped in *api.Error so the gplay
// exit-code taxonomy is honored end-to-end.
func GetDefaultLanguage(ctx context.Context, hc *http.Client, pkg, editID string) (string, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/details"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return "", &api.Error{Operation: "edits.details.get", Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", &api.Error{Operation: "edits.details.get", Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return "", &api.Error{
			Operation:  "edits.details.get",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	var parsed struct {
		DefaultLanguage string `json:"defaultLanguage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", &api.Error{
			Operation:  "edits.details.get",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	if parsed.DefaultLanguage == "" {
		// An empty defaultLanguage with a 2xx response is an API
		// contract violation — downstream callers would emit release
		// notes with empty language keys.
		return "", &api.Error{
			Operation:  "edits.details.get",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "missing defaultLanguage in response body",
		}
	}
	return parsed.DefaultLanguage, nil
}
