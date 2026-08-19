package api_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// FuzzParseErrorEnvelope fuzzes the Google API error-envelope parser, which
// consumes non-2xx response bodies gplay does not control: a malformed or
// hostile server can return anything. The invariant that must hold for every
// input: the parser never panics, and it always yields a non-empty message
// (the fallback chain guarantees at least "HTTP <status>"), so a caller is
// never left with a blank error string. Seeded from the existing error-body
// fixtures plus malformed-input edge cases.
func FuzzParseErrorEnvelope(f *testing.F) {
	seeds := []struct {
		body   string
		status int
	}{
		{`{"error":{"code":403,"message":"Service account is not invited on the app"}}`, 403},
		{`{"error":{"code":409,"message":"edit conflict","errors":[{"reason":"editAlreadyExists"}]}}`, 409},
		{`{"error":{"code":400}}`, 400},
		{`{"error":{"message":""}}`, 400},
		{`{"error":{"message":"   "}}`, 500},
		{`{"error":{"errors":[{"reason":"rateLimitExceeded"},{"reason":""}]}}`, 429},
		{``, 503},
		{`   `, 500},
		{`not json at all`, 502},
		{`{"error":`, 500},
		{`{"error":{"errors":[]}}`, 400},
		{`{"error":{"message":"unicode é 日本 🎉 stays intact"}}`, 400},
		{"{\"error\":{\"message\":\"raw \x1b[31m ansi and \x00 nul bytes\"}}", 400},
	}
	for _, s := range seeds {
		f.Add([]byte(s.body), s.status)
	}
	f.Fuzz(func(t *testing.T, body []byte, status int) {
		msg, reasons := api.ParseErrorEnvelope(body, status)
		if msg == "" {
			t.Errorf("ParseErrorEnvelope returned an empty message for body=%q status=%d (the fallback chain must always yield a non-empty string)", body, status)
		}
		for i, r := range reasons {
			if r == "" {
				t.Errorf("ParseErrorEnvelope returned an empty reason at index %d for body=%q (empty reasons must be filtered out)", i, body)
			}
		}
	})
}
