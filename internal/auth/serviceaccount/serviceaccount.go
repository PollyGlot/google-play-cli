// Package serviceaccount loads and validates Google Cloud service-account
// JSON files. It performs pure parsing — no network, no token minting.
package serviceaccount

import (
	"encoding/json"
	"fmt"
	"os"
)

// MissingFieldError is returned when a required service-account JSON field
// (client_email, private_key, token_uri, project_id) is absent or empty.
// The command layer maps this to exit code 10 with a hint naming the field.
type MissingFieldError struct {
	Field string
}

func (e *MissingFieldError) Error() string {
	return fmt.Sprintf("service account JSON: missing or empty required field %q", e.Field)
}

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

// Load reads and parses a service-account JSON file from disk.
func Load(path string) (*ServiceAccount, error) {
	data, err := os.ReadFile(path)
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
