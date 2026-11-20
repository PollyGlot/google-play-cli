package testkit

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"sync"
	"testing"
	"time"
)

const (
	ProjectID   = "p"
	ClientEmail = "ci@p.iam.gserviceaccount.com"
)

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

var sharedCert = sync.OnceValues(func() ([]byte, error) {
	key, err := sharedKey()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "gplay test"},
		NotBefore:    time.Unix(0, 0).UTC(),
		NotAfter:     time.Unix(0, 0).UTC().AddDate(100, 0, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
})

func CertificatePEM(t testing.TB) []byte {
	t.Helper()
	b, err := sharedCert()
	if err != nil {
		t.Fatalf("testkit: create certificate: %v", err)
	}
	return append([]byte(nil), b...)
}

func RSAKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	key, err := sharedKey()
	if err != nil {
		t.Fatalf("testkit: generate RSA key: %v", err)
	}
	return key
}

func PrivateKeyPEM(t testing.TB) []byte {
	t.Helper()
	b, err := sharedPEM()
	if err != nil {
		t.Fatalf("testkit: encode RSA key: %v", err)
	}
	return append([]byte(nil), b...)
}

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
