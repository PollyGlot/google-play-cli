// Package listings reads and writes the per-locale Store front Listings of
// a Google Play app within an open Edit. The operations exposed — List
// (every locale's Listing), Get (one locale), Patch (upsert one locale),
// and Delete (drop one locale's whole Listing) — back the metadata
// list / pull / apply commands. Every function runs inside an Edit the
// caller has already opened (edits.WithEdit / edits.WithReadOnlyEdit);
// none opens or commits an Edit of its own.
package listings

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opListingsList   = "listings.list"
	opListingsGet    = "listings.get"
	opListingsPatch  = "listings.patch"
	opListingsDelete = "listings.delete"
)

// Listing is the API-shaped edits.listings resource: one per locale. The
// json tags mirror the Google Play Developer API verbatim per ADR-0003
// (JSON output is API pass-through). Only the text fields gplay manages
// are modeled; store images (edits.images) are reserved future scope.
type Listing struct {
	Language         string `json:"language"`
	Title            string `json:"title"`
	FullDescription  string `json:"fullDescription"`
	ShortDescription string `json:"shortDescription"`
	Video            string `json:"video"`
}

// List fetches every locale's Listing at edits.listings.list. The API
// envelope is {"listings":[ {...}, ... ], "kind":"..."}; List parses
// `.listings` into a Listing slice and returns the raw body verbatim for
// the --output json pass-through (ADR-0003). Like the rest of the package
// it runs inside an Edit the caller has already opened.
func List(ctx context.Context, hc *http.Client, pkg, editID string) ([]Listing, json.RawMessage, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/listings"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, &api.Error{Operation: opListingsList, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &api.Error{Operation: opListingsList, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, nil, &api.Error{
			Operation:  opListingsList,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, nil, &api.Error{
			Operation:  opListingsList,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "read response: " + readErr.Error(),
			Cause:      readErr,
		}
	}
	var parsed struct {
		Listings []Listing `json:"listings"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, &api.Error{
			Operation:  opListingsList,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return parsed.Listings, raw, nil
}

// Get fetches a single locale's Listing at edits.listings.get. Returns the
// parsed Listing and the raw JSON body for the --output json pass-through
// (ADR-0003). A 404 here means "no Listing for this locale" — the caller
// maps it via the gplay exit-code taxonomy.
func Get(ctx context.Context, hc *http.Client, pkg, editID, language string) (*Listing, json.RawMessage, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/listings/" + url.PathEscape(language)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, &api.Error{Operation: opListingsGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &api.Error{Operation: opListingsGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, nil, &api.Error{
			Operation:  opListingsGet,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, nil, &api.Error{
			Operation:  opListingsGet,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "read response: " + readErr.Error(),
			Cause:      readErr,
		}
	}
	var parsed Listing
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, &api.Error{
			Operation:  opListingsGet,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return &parsed, raw, nil
}

// Patch upserts one locale's Listing at edits.listings.patch, sending the
// caller-built body verbatim. The body is built by the caller (the apply
// command) and carries exactly the fields it means to write; PATCH (not
// PUT) is used on purpose so a field absent from the body is left
// untouched online — the ADR-0011 "missing ≠ empty" rule, enforced at the
// wire level. Returns the raw response body (the patched Listing) for the
// per-locale --output json pass-through (ADR-0003).
func Patch(ctx context.Context, hc *http.Client, pkg, editID, language string, body []byte) (json.RawMessage, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/listings/" + url.PathEscape(language)

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, bytes.NewReader(body))
	if err != nil {
		return nil, &api.Error{Operation: opListingsPatch, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: opListingsPatch, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(errBody, resp.StatusCode)
		return nil, &api.Error{
			Operation:  opListingsPatch,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	// The success body is the ADR-0003 per-locale JSON pass-through; cap
	// it at MaxAPISuccessBodyRead — a fullDescription alone can run to
	// 4000 chars, comfortably past the error-body cap.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	return raw, nil
}

// Delete drops a whole locale's Listing at edits.listings.delete: every
// managed field for that locale is removed online in one call. This is the
// operation ADR-0011 calls "deletegroup" — there is no separate
// edits.listings.deletegroup endpoint; the real androidpublisher surface
// that deletes a single locale's Listing is edits.listings.delete, and
// that is what gplay calls. A 2xx/204 with no body is expected. Op
// "listings.delete".
func Delete(ctx context.Context, hc *http.Client, pkg, editID, language string) error {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/listings/" + url.PathEscape(language)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return &api.Error{Operation: opListingsDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &api.Error{Operation: opListingsDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return &api.Error{
			Operation:  opListingsDelete,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	return nil
}
