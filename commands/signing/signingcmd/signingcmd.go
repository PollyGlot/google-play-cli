// Package signingcmd holds the wiring shared by the `gplay signing` leaves:
// PEM/lineage file reading, the --confirm gate, the certificate-hash table
// (ADR-0018) and 404/403 hint classification. Mirrors
// commands/recovery/recoverycmd.
package signingcmd

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// RequireConfirm enforces the DESTRUCTIVE-tier --confirm gate (ADR-0017/0043):
// both signing leaves change the live signing key of a real app, which is
// irreversible and externally visible, so they refuse with exit 3 (a
// deterministically resolvable *exit.SafetyFlagError naming the flag) unless
// --confirm is passed. The LIVE path calls this; --dry-run never does (it
// reports the requirement through the payload's `requires` array instead).
func RequireConfirm(confirm bool, msg string) error {
	if confirm {
		return nil
	}
	return exit.SafetyFlag("confirm", "%s", msg)
}

// ReadPEM reads a certificate file and hands back the PEM of its certificate
// blocks (the caller passes them straight to the API, which base64-encodes
// them as a `format: byte` field). Taking a path rather than a blob is
// deliberate: nobody should have to paste base64 on a command line.
//
// The bytes land in the body of an irreversible call, so the file is decoded,
// not sniffed: every block must be a CERTIFICATE that x509 parses. A private
// key block (the usual `openssl pkcs12 -nodes` export puts the upload key and
// its certificate in one file) or a service-account JSON (its private_key
// field holds a PEM key) is refused, naming the block type and never quoting
// its bytes. Anything else is caught here instead of as an opaque 400. Text
// around the blocks (openssl "Bag Attributes") is dropped: only re-encoded
// certificate blocks are forwarded.
func ReadPEM(flag, path string) ([]byte, error) {
	b, err := ReadFile(flag, path)
	if err != nil {
		return nil, err
	}
	hint := certHint(flag)
	if json.Valid(b) {
		return nil, exit.Usagef("--%s: %s is a JSON file (a service-account key?), not a certificate: %s", flag, path, hint)
	}
	var out []byte
	for rest := b; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if strings.Contains(block.Type, "PRIVATE KEY") {
			return nil, exit.Usagef("--%s: %s contains a %q block: a private key must never be sent to Google Play; %s", flag, path, block.Type, hint)
		}
		if block.Type != "CERTIFICATE" {
			return nil, exit.Usagef("--%s: %s contains a %q block, not a certificate: %s", flag, path, block.Type, hint)
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, exit.Usagef("--%s: %s holds a CERTIFICATE block that is not a valid X.509 certificate (%v): %s", flag, path, err, hint)
		}
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: block.Bytes})...)
	}
	if len(out) == 0 {
		return nil, exit.Usagef("--%s: %s is not a PEM certificate (no \"-----BEGIN CERTIFICATE-----\" block): %s", flag, path, hint)
	}
	return out, nil
}

// certHint says what to pass instead, per flag: the upload certificate comes
// out of the developer's own keystore, the KMS one alongside the key version.
func certHint(flag string) string {
	if flag == "upload-cert" {
		return "pass the upload key's certificate alone, in PEM (e.g. `keytool -export -rfc -keystore upload.jks -alias upload -file upload_cert.pem`)"
	}
	return "pass the X.509 certificate of the Cloud KMS key version alone, in PEM (\"-----BEGIN CERTIFICATE-----\" blocks only)"
}

// ReadFile reads an input file, turning an unreadable path into a usage error
// naming the flag rather than a bare filesystem error.
func ReadFile(flag, path string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own flag value
	if err != nil {
		return nil, exit.Usagef("--%s: cannot read %s: %v", flag, path, err)
	}
	if len(b) == 0 {
		return nil, exit.Usagef("--%s: %s is empty", flag, path)
	}
	return b, nil
}

// packageNotFoundError / forbiddenError attach actionable hints, leaving the
// wrapped *api.Error to drive the exit code.
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("app %q not found: verify the package name with `gplay apps list` (appsigning also accepts an app ID): %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not allowed to manage app signing for %q: it needs app access in the Play Console (Setup → API access) AND the Cloud KMS key must grant Google Play the Decrypt and Sign permissions: %v", e.pkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// Classify adds 404/403 hints, leaving the *api.Error to drive the exit code.
func Classify(pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return &packageNotFoundError{pkg: pkg, cause: err}
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		}
	}
	return err
}
