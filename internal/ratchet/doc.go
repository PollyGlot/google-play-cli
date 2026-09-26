// Package ratchet holds no production code: its test forbids new copies of the
// patterns the paved road (PRD #573) replaced, so a new command cannot copy
// its neighbour's private request helper, test RoundTripper, key generation,
// http.Client or usage-error type.
//
// Each rule parses the Go files under cmd/, commands/ and internal/ and
// compares what it finds with a committed allowlist of today's offenders,
// testdata/<rule>.allow, one `path symbol` per line. The comparison fails both
// ways: an offender missing from the list fails (a new copy), and a line that
// no longer matches an offender fails too (the copy was migrated, so the line
// must go). The lists can therefore only shrink. A rule with no offenders left
// has no allowlist file at all: it forbids outright.
//
// The rules and their replacements:
//
//   - request-helper: a direct (*http.Client).Do outside internal/play/api.
//     Send the request through the executor, api.Do / api.DoJSON.
//   - test-roundtripper: a RoundTrip method declared by test code outside
//     internal/testkit. Use testkit.NewFake, or testkit.TokenResponse and
//     testkit.Response; extend the kit when it cannot express a case.
//   - test-rsa-keygen: rsa.GenerateKey in test code outside internal/testkit.
//     Use testkit.RSAKey, PrivateKeyPEM or ServiceAccountJSON.
//   - http-client: an http.Client constructed by shipped code outside
//     internal/transport. Take the client the RunContext builds
//     (rc.AuthedClient / rc.UploadClient) and wrap transports in
//     internal/transport, whose NewClient is the one constructor.
//   - usage-error-type: a local usage-error type. Return exit.UsageError
//     (exit.Usagef).
//
// `make ratchets` prints the allowlist size of every rule, for a before/after
// report. The literal-API-path gate that this package generalises stays in
// internal/apiregistry/archgate_test.go, next to the registry it protects.
package ratchet
