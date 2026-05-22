// Package serviceaccount loads and validates Google Cloud service-account
// JSON files. It performs pure parsing — no network, no token minting.
package serviceaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// FileReader is the slice of config.FS this package needs. Declared
// here as a tiny interface so internal/auth/serviceaccount stays
// import-free of internal/config; production callers pass config.OSFS{}
// and tests pass config.MemFS or any other matching implementation.
type FileReader interface {
	ReadFile(name string) ([]byte, error)
}

// MissingFieldError is returned when a required service-account JSON field
// (client_email, private_key, token_uri, project_id) is absent or empty.
// The command layer maps this to exit code 10 with a hint naming the field.
type MissingFieldError struct {
	Field string
}

func (e *MissingFieldError) Error() string {
	return fmt.Sprintf("service account JSON: missing or empty required field %q", e.Field)
}

// ExitCode satisfies exit.Coder: a malformed credential is an auth failure.
func (*MissingFieldError) ExitCode() int { return 10 }

// ServiceAccount holds the credential fields gplay needs to mint an OAuth2
// token for the Google Play Developer API.
type ServiceAccount struct {
	ClientEmail string
	PrivateKey  string
	TokenURI    string
	ProjectID   string

	// Raw retains the original bytes so callers (e.g. the token package) can
	// hand them directly to google.JWTConfigFromJSON without reserializing.
	Raw []byte
}

// Load reads and parses a service-account JSON file from disk via
// os.ReadFile. Use LoadFromFS when the caller can inject a FileReader
// (kernel-driven paths do).
func Load(path string) (*ServiceAccount, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// LoadFromFS reads a service-account JSON file through fr and parses
// it. ctx is threaded for future cancellation support; the FileReader
// itself is synchronous today.
func LoadFromFS(_ context.Context, fr FileReader, path string) (*ServiceAccount, error) {
	data, err := fr.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse validates raw service-account JSON bytes.
func Parse(data []byte) (*ServiceAccount, error) {
	var raw struct {
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
		ProjectID   string `json:"project_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for _, f := range [...]struct {
		name  string
		value string
	}{
		{"client_email", raw.ClientEmail},
		{"private_key", raw.PrivateKey},
		{"token_uri", raw.TokenURI},
		{"project_id", raw.ProjectID},
	} {
		if f.value == "" {
			return nil, &MissingFieldError{Field: f.name}
		}
	}
	return &ServiceAccount{
		ClientEmail: raw.ClientEmail,
		PrivateKey:  raw.PrivateKey,
		TokenURI:    raw.TokenURI,
		ProjectID:   raw.ProjectID,
		Raw:         data,
	}, nil
}
