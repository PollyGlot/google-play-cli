package signingcmd_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/signing/signingcmd"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/appsigning"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// errRT answers every request with the configured status and body: enough to
// drive the *api.Error path offline.
type errRT struct {
	status int
	body   string
}

func (e errRT) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: e.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(e.body)),
	}, nil
}

// TestEnroll_nonSuccessBecomesApiError asserts a refusal surfaces as *api.Error
// tagged with the REST method id, so the exit-code taxonomy maps transparently.
func TestEnroll_nonSuccessBecomesApiError(t *testing.T) {
	hc := &http.Client{Transport: errRT{status: http.StatusForbidden, body: `{"error":{"code":403,"message":"caller lacks permission"}}`}}
	_, _, err := appsigning.Enroll(context.Background(), hc, "com.example.app", appsigning.EnrollOpts{KmsKeyResource: "k"})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
	if apiErr.Operation != "appsigning.enrollApp" {
		t.Errorf("Operation = %q, want appsigning.enrollApp", apiErr.Operation)
	}
}

// TestClassify_addsActionableHints asserts a 404 and a 403 become messages an
// agent can act on, while keeping the wrapped *api.Error's exit code.
func TestClassify_addsActionableHints(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusNotFound, "gplay apps list"},
		{http.StatusForbidden, "Decrypt and Sign"},
	}
	for _, tc := range cases {
		cause := &api.Error{Operation: "appsigning.enrollApp", StatusCode: tc.status, Message: "boom"}
		got := signingcmd.Classify("com.example.app", cause)
		if !strings.Contains(got.Error(), tc.want) {
			t.Errorf("Classify(%d) = %v, should hint %q", tc.status, got, tc.want)
		}
		var apiErr *api.Error
		if !errors.As(got, &apiErr) {
			t.Errorf("Classify(%d) must keep the *api.Error unwrappable", tc.status)
		}
	}
	// An error that is not an *api.Error passes through untouched.
	plain := errors.New("dial tcp: no route to host")
	if got := signingcmd.Classify("com.example.app", plain); !errors.Is(got, plain) {
		t.Errorf("Classify must pass a non-API error through: %v", got)
	}
}

// TestRotationReasons_coverTheApiEnumMinusUnspecified pins the vocabulary: the
// UNSPECIFIED value is deliberately not offerable, the API rejects it.
func TestRotationReasons_coverTheApiEnumMinusUnspecified(t *testing.T) {
	got := appsigning.RotationReasons()
	want := []string{"compromised-key", "other", "routine-key-upgrade", "use-same-key-for-multiple-apps", "use-stronger-key"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("RotationReasons() = %v, want %v (sorted)", got, want)
	}
	for _, choice := range got {
		enum, ok := appsigning.RotationReason(choice)
		if !ok || enum == "" || enum == "KEY_ROTATION_REASON_UNSPECIFIED" {
			t.Errorf("RotationReason(%q) = %q, %v", choice, enum, ok)
		}
	}
	if _, ok := appsigning.RotationReason("KEY_ROTATION_REASON_UNSPECIFIED"); ok {
		t.Error("the UNSPECIFIED enum must not be an accepted --reason choice")
	}
}

// TestReadPEM_acceptsOnlyCertificates pins SEC-08 (#589): the file's bytes go
// into the body of an irreversible call, so ReadPEM decodes it and refuses a
// private key or a service-account JSON, naming what to pass instead and never
// echoing the key material. Surrounding text (openssl "Bag Attributes") is
// dropped and only the certificate blocks are forwarded.
func TestReadPEM_acceptsOnlyCertificates(t *testing.T) {
	cert := string(testkit.CertificatePEM(t))
	key := string(testkit.PrivateKeyPEM(t))
	rsaKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(testkit.RSAKey(t))}))
	pub := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("x")}))
	keyBody := strings.Split(key, "\n")[1] // a line of base64 key material

	cases := []struct {
		name     string
		content  string
		wantErr  []string // substrings of the usage error; nil = accepted
		wantBody string
	}{
		{name: "one certificate", content: cert, wantBody: cert},
		{name: "certificate chain", content: cert + cert, wantBody: cert + cert},
		{name: "openssl bag attributes around the cert", content: "Bag Attributes\n    friendlyName: upload\n" + cert, wantBody: cert},
		{name: "pkcs12 -nodes export: key and cert", content: "Bag Attributes\n" + key + cert, wantErr: []string{`"PRIVATE KEY" block`, "never be sent", "keytool -export -rfc"}},
		{name: "key only", content: key, wantErr: []string{`"PRIVATE KEY" block`}},
		{name: "PKCS#1 key", content: rsaKey, wantErr: []string{`"RSA PRIVATE KEY" block`}},
		{name: "service-account JSON", content: string(testkit.ServiceAccountJSON(t)), wantErr: []string{"JSON file", "service-account", "certificate"}},
		{name: "public key", content: pub, wantErr: []string{`"PUBLIC KEY" block, not a certificate`}},
		{name: "unparseable certificate", content: "-----BEGIN CERTIFICATE-----\nZmFrZQ==\n-----END CERTIFICATE-----\n", wantErr: []string{"not a valid X.509 certificate"}},
		{name: "plain text", content: "not a pem file", wantErr: []string{"not a PEM certificate"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cert.pem")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := signingcmd.ReadPEM("upload-cert", path)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ReadPEM: %v", err)
				}
				if string(got) != tc.wantBody {
					t.Errorf("forwarded %q, want %q", got, tc.wantBody)
				}
				return
			}
			var coder interface{ ExitCode() int }
			if !errors.As(err, &coder) || coder.ExitCode() != 2 {
				t.Fatalf("err = %v, want a usage error (exit 2)", err)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			if strings.Contains(err.Error(), keyBody) {
				t.Errorf("error echoes private key material: %q", err)
			}
		})
	}
}
