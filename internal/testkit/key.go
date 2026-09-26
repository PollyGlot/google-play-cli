// Package testkit is the shared test harness for gplay's offline suites: a
// throwaway service-account fixture whose RSA key is generated once per test
// binary, a fake Play transport that answers the OAuth2 token exchange only
// at its exact URL, and a golden-file helper.
//
// It imports nothing from this module, so any package (internal/kernel and
// internal/auth included) can use it from its own tests without an import
// cycle. RunContext wiring stays in the callers (teamtest, gamescmdtest).
//
// Production code must not import this package.
package testkit

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"sync"
	"testing"
)

// Fixture identity of the service account ServiceAccountJSON mints.
const (
	ProjectID   = "p"
	ClientEmail = "ci@p.iam.gserviceaccount.com"
)

// sharedKey is generated on first use and reused by every test in the binary:
// an RSA-2048 keygen costs about 60 ms (170 ms under -race), and minting one
// per test was nearly the whole CPU cost of the suite. Generating it at run
// time rather than committing a PEM keeps secret scanners and the redact
// patterns quiet. No test depends on key uniqueness; *rsa.PrivateKey is safe
// for concurrent signing once generated, so parallel tests may share it.
var sharedKey = sync.OnceValues(func() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
})

var sharedPEM = sync.OnceValues(func() ([]byte, error) {
	key, err := sharedKey()
	if err != nil {
		return nil, err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), nil
})

// RSAKey returns the RSA-2048 key shared by every test in the binary. Callers
// must not mutate it.
func RSAKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	key, err := sharedKey()
	if err != nil {
		t.Fatalf("testkit: generate RSA key: %v", err)
	}
	return key
}

// PrivateKeyPEM returns RSAKey as a PKCS#8 "PRIVATE KEY" PEM block, the form
// a service-account JSON carries. The slice is a fresh copy.
func PrivateKeyPEM(t testing.TB) []byte {
	t.Helper()
	b, err := sharedPEM()
	if err != nil {
		t.Fatalf("testkit: encode RSA key: %v", err)
	}
	return append([]byte(nil), b...)
}

// ServiceAccountJSON mints a service-account key file backed by RSAKey, with
// token_uri set to TokenURL so the token exchange lands on the fake transport.
func ServiceAccountJSON(t testing.TB) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":         "service_account",
		"project_id":   ProjectID,
		"private_key":  string(PrivateKeyPEM(t)),
		"client_email": ClientEmail,
		"token_uri":    TokenURL,
	})
	if err != nil {
		t.Fatalf("testkit: marshal service account: %v", err)
	}
	return raw
}
