// Package api owns the shared low-level concerns of every internal/play/*
// module: the canonical androidpublisher base URLs, the per-response
// body-size caps, and the HTTP status → gplay exit-code mapping plus the
// API error-envelope parser.
package api

const (
	// AndroidPubBase is the data-plane base URL for the Google Play
	// Developer API v3 (edits, tracks, listings, details, ...).
	AndroidPubBase = "https://androidpublisher.googleapis.com/androidpublisher/v3"

	// UploadBase is the media-upload base URL for the same service.
	// Used by bundles.upload (and, later, deobfuscationfiles).
	UploadBase = "https://androidpublisher.googleapis.com/upload/androidpublisher/v3"

	// ReportingBase is the data-plane base URL for the Play Developer Reporting
	// API v1beta1 — a DISTINCT Google service (its own host and OAuth scope)
	// carrying the read-only post-launch quality surface (crashes/ANR vitals,
	// anomalies, error reports; #49). Resource paths hang off this base, e.g.
	// `/apps/{package}/crashRateMetricSet:query`.
	ReportingBase = "https://playdeveloperreporting.googleapis.com/v1beta1"

	// CustomAppUploadBase is the media-upload base URL for the Play Custom App
	// Publishing API (playcustomapp) — a DISTINCT Google service (its own host,
	// the androidpublisher OAuth scope) whose entire current surface is one
	// account-axis method, accounts.customApps.create (ADR-0032). That method is
	// a multipart media upload, so only the /upload base is needed; resource
	// paths hang off it, e.g. `/accounts/{account}/customApps`.
	CustomAppUploadBase = "https://playcustomapp.googleapis.com/upload/playcustomapp/v1"

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
	// (4 MiB) — enough headroom for any sane Play response while still
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
