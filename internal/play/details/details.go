// Package details reads app-level metadata. Two entry points coexist:
//
//   - GetDefaultLanguage is the low-level helper used inside an already-
//     open Edit (internal/releases/notes resolves the locale for a single
//     --release-notes text). It does one GET on edits.details.get and
//     returns just the defaultLanguage string.
//
//   - Get is the high-level entry point used by `gplay apps view`. It
//     opens a read-only Edit on its own, fetches details.get +
//     listings.get(defaultLanguage) + images.list(defaultLanguage, icon),
//     and discards the Edit. Returns the typed *Details plus the raw
//     JSON envelope {"details":..,"listing":..,"icon"?:..} (explicit
//     exception to ADR-0003 because multiple endpoints are merged; the
//     [experimental] icon key is omitted when the icon slot is empty).
package details

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

const (
	opDetailsGet   = "details.get"
	opDetailsPatch = "details.patch"
	opListingsGet  = "listings.get"
)

// Registry entries this package calls. Resolving them at init makes an
// unregistered or vanished method a startup panic caught by CI rather than a
// runtime surprise; verb and URL then come from the Discovery snapshot instead
// of literals kept here (#513).
var (
	methodDetailsGet   = apiregistry.MustResolve("androidpublisher.edits.details.get")
	methodDetailsPatch = apiregistry.MustResolve("androidpublisher.edits.details.patch")
	methodListingsGet  = apiregistry.MustResolve("androidpublisher.edits.listings.get")
)

// Details surfaces the fields `gplay apps view` displays: the minimum
// needed to confirm "yes, I'm looking at the right app".
// defaultLanguage and contactEmail come from edits.details.get; title
// comes from edits.listings.get on the default language; Icon is the
// optional [experimental] icon carrier (nil when the default language's
// icon slot is empty), read from edits.images.list on the default
// language.
type Details struct {
	DefaultLanguage string `json:"defaultLanguage"`
	Title           string `json:"title"`
	ContactEmail    string `json:"contactEmail"`
	Icon            *Icon  `json:"-"`
}

// Icon is the optional identity handle for an app's store icon on its
// default language: the durable content sha256 plus the (ephemeral,
// never-persist) preview url. Both are verbatim edits.images field
// values (ADR-0038). nil on Details when the icon slot is empty.
type Icon struct {
	URL    string `json:"url"`
	Sha256 string `json:"sha256"`
}

// AppDetails is the full edits.details resource backing the `apps details`
// group: the app-global App details record. Unlike Details (the
// cross-resource identity card behind apps view, which borrows title
// from a Listing), AppDetails is exactly the four fields the
// edits.details resource owns. json tags mirror the API verbatim so the
// --output json pass-through (ADR-0003) carries the upstream field names
// unchanged, and since GetDetails reads a SINGLE endpoint, no envelope
// exception is needed.
type AppDetails struct {
	DefaultLanguage string `json:"defaultLanguage"`
	ContactEmail    string `json:"contactEmail"`
	ContactPhone    string `json:"contactPhone"`
	ContactWebsite  string `json:"contactWebsite"`
}

// GetDetails opens a read-only Edit on pkg, reads edits.details.get ONLY
// (no listings.get, unlike Get), discards the Edit, and returns both the
// typed *AppDetails and the raw details.get body verbatim. Because a
// single endpoint is read, the raw payload is a clean ADR-0003
// pass-through: there is no gplay envelope to document an exception for.
//
// Errors propagate as *api.Error so the gplay exit-code taxonomy maps
// transparently: 403 → 11, 404 → 30, 5xx → 40, network → 50. The Edit is
// always discarded (WithReadOnlyEdit's deferred cleanup) even on failure
// : a dangling read-only Edit would block the user's next publish for
// ~24h. Unlike fetchDetails (whose empty-defaultLanguage guard exists to
// protect a downstream listings.get URL), GetDetails surfaces whatever
// the API returns: there is no second call to protect, and the read must
// report the resource faithfully.
func GetDetails(ctx context.Context, hc *http.Client, pkg string) (*AppDetails, json.RawMessage, error) {
	var (
		out *AppDetails
		raw json.RawMessage
	)
	if err := edits.WithReadOnlyEdit(ctx, hc, pkg, func(editID string) error {
		u, err := methodDetailsGet.URL(map[string]string{"packageName": pkg, "editId": editID})
		if err != nil {
			return &api.Error{Operation: opDetailsGet, Package: pkg, Message: err.Error(), Cause: err}
		}
		body, status, err := getJSON(ctx, hc, methodDetailsGet.Verb, opDetailsGet, pkg, u)
		if err != nil {
			return err
		}
		var parsed AppDetails
		if err := json.Unmarshal(body, &parsed); err != nil {
			return &api.Error{
				Operation:  opDetailsGet,
				Package:    pkg,
				StatusCode: status,
				Message:    "decode response: " + err.Error(),
				Cause:      err,
			}
		}
		out, raw = &parsed, body
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return out, raw, nil
}

// Get opens a read-only Edit on pkg, reads details.get +
// listings.get(defaultLanguage) + images.list(defaultLanguage, icon),
// discards the Edit, and returns both the typed *Details and the raw
// JSON envelope. The envelope shape is
// {"details": <details.get verbatim>, "listing": <listings.get verbatim>,
// "icon": {"url":..,"sha256":..}}: an explicit exception to ADR-0003
// documented in the ADR's Exceptions section, because apps view combines
// multiple endpoints. The [experimental] icon key carries verbatim
// edits.images field values and is omitted entirely when the default
// language's icon slot is empty (missing == empty, ADR-0013).
//
// Errors propagate as *api.Error so the gplay exit-code taxonomy maps
// transparently: 403 → 11, 404 → 30, 5xx → 40, network → 50. The Edit
// is always discarded (WithReadOnlyEdit's deferred cleanup) even on
// failure: a dangling Edit would block the user's next publish for ~24h.
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
		// [experimental] optional icon read, inside the SAME read-only
		// Edit. edits.images.list on the default language's icon slot:
		// missing == empty (ADR-0013), so an absent icon returns an
		// empty slice and no error: the key is then omitted entirely.
		// Faithful live read, no cache (ADR-0038).
		icon, err := fetchIcon(ctx, hc, pkg, editID, defaultLang)
		if err != nil {
			return err
		}
		envelope, err := json.Marshal(struct {
			Details json.RawMessage `json:"details"`
			Listing json.RawMessage `json:"listing"`
			Icon    *Icon           `json:"icon,omitempty"`
		}{
			Details: detailsRaw,
			Listing: listingRaw,
			Icon:    icon,
		})
		if err != nil {
			// Operation names elsewhere in internal/play/* match a real
			// upstream endpoint (edits.insert, tracks.get, …) so log
			// readers can correlate them with the Google Play API
			// reference. The envelope marshal happens locally and has
			// no upstream peer; use a "details." prefix so a reader
			// sees "this came from the details package, not from a
			// listings.get response we mishandled". Defensive: both
			// inputs are json.RawMessage and can't actually fail
			// json.Marshal, but if the struct ever sprouts a typed
			// field, the tag stays in-domain.
			return &api.Error{Operation: "details.envelope", Package: pkg, Message: "marshal envelope: " + err.Error(), Cause: err}
		}
		out = &Details{
			DefaultLanguage: defaultLang,
			Title:           title,
			ContactEmail:    contactEmail,
			Icon:            icon,
		}
		raw = envelope
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return out, raw, nil
}

// AppDetailsPatch is a partial update of the edits.details resource. Each
// field is a *string with the missing-vs-empty contract (ADR-0011):
//   - nil   → the field is OMITTED from the patch body (left intact upstream)
//   - non-nil → the field is SENT (including a pointer to "", which CLEARS it)
//
// The json tags carry ,omitempty so encoding/json drops nil pointers but
// keeps a non-nil pointer to the empty string: a nil pointer is "empty"
// and omitted, while a non-nil *string is never empty regardless of the
// string it points at. That is exactly the "set what you're given, leave
// the rest" semantics: no manual map-building needed.
type AppDetailsPatch struct {
	DefaultLanguage *string `json:"defaultLanguage,omitempty"`
	ContactEmail    *string `json:"contactEmail,omitempty"`
	ContactPhone    *string `json:"contactPhone,omitempty"`
	ContactWebsite  *string `json:"contactWebsite,omitempty"`
}

// Patch PATCHes a partial AppDetailsPatch to edits.details.patch inside an
// Edit the caller has already opened. Only the non-nil fields of patch
// reach the wire (see AppDetailsPatch): a field the caller left nil is
// absent from the body and stays intact upstream; a field set to a
// pointer-to-"" is sent empty and clears it. Returns the parsed
// *AppDetails (the patched resource the API echoes back) and the raw JSON
// body for the --output json pass-through (ADR-0003). Errors propagate as
// *api.Error so the exit-code taxonomy maps transparently. Modeled on
// testers.Update, but a PATCH (partial) rather than a PUT (wholesale).
func Patch(ctx context.Context, hc *http.Client, pkg, editID string, patch AppDetailsPatch) (*AppDetails, json.RawMessage, error) {
	payload, err := json.Marshal(patch)
	if err != nil {
		return nil, nil, &api.Error{Operation: opDetailsPatch, Package: pkg, Message: "marshal payload: " + err.Error(), Cause: err}
	}

	u, err := methodDetailsPatch.URL(map[string]string{"packageName": pkg, "editId": editID})
	if err != nil {
		return nil, nil, &api.Error{Operation: opDetailsPatch, Package: pkg, Message: err.Error(), Cause: err}
	}

	req, err := http.NewRequestWithContext(ctx, methodDetailsPatch.Verb, u, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, &api.Error{Operation: opDetailsPatch, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &api.Error{Operation: opDetailsPatch, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Mirror getJSON's defensive read: a truncated/interrupted read on
		// the error path would otherwise mask the real reason behind a
		// generic ParseErrorEnvelope fallback. Surface it verbatim so the
		// operator knows the request reached the server but the stream broke.
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		if readErr != nil {
			return nil, nil, &api.Error{
				Operation:  opDetailsPatch,
				Package:    pkg,
				StatusCode: resp.StatusCode,
				Message:    "read error response body: " + readErr.Error(),
				Cause:      readErr,
			}
		}
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, nil, &api.Error{
			Operation:  opDetailsPatch,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	// Same defensive read on the success path: a partial JSON body would
	// otherwise reach json.Unmarshal and surface as a "decode response"
	// error that buries the real (network) cause.
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, nil, &api.Error{
			Operation:  opDetailsPatch,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "read response body: " + readErr.Error(),
			Cause:      readErr,
		}
	}
	var parsed AppDetails
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, &api.Error{
			Operation:  opDetailsPatch,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return &parsed, raw, nil
}

// fetchDetails GETs edits.details.get and returns (raw body,
// defaultLanguage, contactEmail, err). The raw body is what feeds the
// `"details"` slot of the apps view envelope.
func fetchDetails(ctx context.Context, hc *http.Client, pkg, editID string) (json.RawMessage, string, string, error) {
	u, err := methodDetailsGet.URL(map[string]string{"packageName": pkg, "editId": editID})
	if err != nil {
		return nil, "", "", &api.Error{Operation: opDetailsGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	raw, status, err := getJSON(ctx, hc, methodDetailsGet.Verb, opDetailsGet, pkg, u)
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
	u, err := methodListingsGet.URL(map[string]string{"packageName": pkg, "editId": editID, "language": language})
	if err != nil {
		return nil, "", &api.Error{Operation: opListingsGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	raw, status, err := getJSON(ctx, hc, methodListingsGet.Verb, opListingsGet, pkg, u)
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

// fetchIcon reads the default language's icon slot via edits.images.list
// inside the already-open Edit and returns the optional *Icon carrier.
// A singular icon slot holds at most one image (ADR-0013); an absent
// icon is missing == empty, so List returns an empty slice and no error
// and fetchIcon returns (nil, nil): the caller omits the key entirely.
// The url + sha256 are verbatim edits.images field values (ADR-0038):
// sha256 is the durable content-identity handle, url an ephemeral
// preview link the caller must never persist. Errors propagate as
// *api.Error (op "images.list") so the exit-code taxonomy maps
// transparently: 403 → 11, 404 → 30, 5xx → 40, network → 50.
func fetchIcon(ctx context.Context, hc *http.Client, pkg, editID, language string) (*Icon, error) {
	imgs, _, err := images.List(ctx, hc, pkg, editID, language, images.Icon)
	if err != nil {
		return nil, err
	}
	if len(imgs) == 0 {
		return nil, nil
	}
	return &Icon{URL: imgs[0].URL, Sha256: imgs[0].Sha256}, nil
}

// getJSON is the shared scaffolding for a GET that returns a JSON body
// inside an open Edit: build the request, run it, on 2xx return the raw
// bytes (capped at MaxAPISuccessBodyRead) AND the HTTP status, on non-2xx
// wrap the error envelope in an *api.Error tagged with op so the exit-code
// taxonomy stays uniform across details.get and listings.get. Returning
// the HTTP status alongside the body lets callers stamp a downstream
// decode-failure *api.Error with the real status (200 in practice, but
// 204 or 206 are valid 2xx the server might emit) rather than hardcoding
// it: keeping parity with GetDefaultLanguage's behavior.
func getJSON(ctx context.Context, hc *http.Client, verb, op, pkg, u string) (json.RawMessage, int, error) {
	req, err := http.NewRequestWithContext(ctx, verb, u, http.NoBody)
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
		// reached the server but the response stream broke: a
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
	u, err := methodDetailsGet.URL(map[string]string{"packageName": pkg, "editId": editID})
	if err != nil {
		return "", &api.Error{Operation: "edits.details.get", Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, methodDetailsGet.Verb, u, http.NoBody)
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
		// contract violation: downstream callers would emit release
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
