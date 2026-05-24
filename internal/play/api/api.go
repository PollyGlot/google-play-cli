// Package api owns the shared low-level concerns of every internal/play/*
// module: the canonical androidpublisher base URLs and the maximum
// response body size. The HTTP status → gplay exit-code mapping and the
// API error-envelope parser will land in Block 4 of the TDD plan; for
// the tracer bullet, only the URL constants are needed.
package api

const (
	// AndroidPubBase is the data-plane base URL for the Google Play
	// Developer API v3 (edits, tracks, listings, details, ...).
	AndroidPubBase = "https://androidpublisher.googleapis.com/androidpublisher/v3"

	// UploadBase is the media-upload base URL for the same service.
	// Used by bundles.upload (and, later, deobfuscationfiles).
	UploadBase = "https://androidpublisher.googleapis.com/upload/androidpublisher/v3"

	// MaxAPIBodyRead caps how many bytes of an androidpublisher API
	// response body we hold in memory. Error envelopes are small; the
	// cap stops a malformed or hostile server from blowing up RAM.
	MaxAPIBodyRead = 64 * 1024
)
