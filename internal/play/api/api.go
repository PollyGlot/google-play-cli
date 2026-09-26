// Package api is the request layer every internal/play/* module sends through:
// one executor that turns a registered API method into a request, sends it,
// and turns the answer into either the verbatim 2xx body or an *Error that
// already knows its exit code. It also keeps the body-size caps, the HTTP
// status to exit-code table, the error-envelope parser and the resumable
// upload protocol.
//
// # The executor
//
// The interface is two functions and one struct:
//
//	raw, err := api.Do(ctx, hc, api.Call{Method: mGet, Params: p, Target: pkg})
//	raw, err := api.DoJSON(ctx, hc, call, &out) // Do, then decode into out
//
// A module declares its methods once (`mGet = apiregistry.MustResolve(id)`)
// and describes each call with a Call: path Params, an optional Query, an
// optional Body (bytes sent verbatim, a *Stream, or a value to JSON-encode),
// and the Target the call addresses. Everything else is behind the seam:
//
//   - The URL and verb come from the registry, never from a literal (the
//     archgate test in internal/apiregistry enforces it).
//   - A Body gets `Content-Type: application/json` unless Call.ContentType
//     says otherwise, and a replayable body, so the --retry transport can
//     re-send it.
//   - A non-2xx answer becomes an *Error carrying the status, the message and
//     the `errors[].reason` values of Google's envelope.
//   - A 2xx body is read up to MaxAPISuccessBodyRead and returned verbatim.
//
// What the executor deliberately does not decide: whether a request may be
// replayed. That is the method's declared Idempotent bit in internal/apiregistry,
// read by the --retry transport (internal/transport) from the request itself,
// so a module gets it right by construction whether or not it has migrated
// onto Do yet. Pagination loops and resumable uploads stay with their callers
// and ResumableUpload respectively.
//
// Base URLs used to live here too. They are gone since #520: a request's
// verb and URL are derived from the Discovery snapshots by
// internal/apiregistry.
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
)
