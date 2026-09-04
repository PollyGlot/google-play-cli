// Package api owns the shared low-level concerns of every internal/play/*
// module: the per-response body-size caps, and the HTTP status → gplay
// exit-code mapping plus the API error-envelope parser.
//
// It used to own the base URLs of each Google service too. Those are gone
// since #520: a request's verb and URL are derived from the Discovery
// snapshots by internal/apiregistry, so no base URL is written by hand any
// more (an archgate test in that package fails on the ones that come back).
package api

const (
	// MaxAPIErrorBodyRead caps how many bytes of a non-2xx androidpublisher
	// response body we hold in memory while parsing the error envelope.
	// Error payloads are tiny ({"error":{"code":...,"message":"..."}});
	// the cap is purely a defence-in-depth against a malformed or hostile
	// server.
	MaxAPIErrorBodyRead = 64 * 1024

	// MaxAPISuccessBodyRead caps how many bytes of a 2xx androidpublisher
	// response body we read for the ADR-0003 JSON pass-through. A
	// tracks.update response for an app with many locales × release-note
	// bodies can comfortably exceed 64 KiB, so we use a much larger cap
	// (4 MiB): enough headroom for any sane Play response while still
	// bounding memory against a runaway server.
	MaxAPISuccessBodyRead = 4 * 1024 * 1024

	// MaxAPIBodyRead is the legacy constant that was used for both error
	// and success bodies. It is preserved as an alias of
	// MaxAPIErrorBodyRead for the existing call sites in
	// internal/play/{edits,tracks,bundles,details}; new code should pick
	// MaxAPIErrorBodyRead or MaxAPISuccessBodyRead explicitly based on
	// whether the response is an error envelope or a JSON pass-through.
	//
	// Deprecated: use MaxAPIErrorBodyRead (for error bodies) or
	// MaxAPISuccessBodyRead (for success bodies) instead.
	MaxAPIBodyRead = MaxAPIErrorBodyRead
)
