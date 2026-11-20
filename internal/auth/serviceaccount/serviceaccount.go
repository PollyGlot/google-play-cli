package serviceaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

type FileReader interface {
	ReadFile(name string) ([]byte, error)
}

type MissingFieldError struct {
	Field string
}

func (e *MissingFieldError) Error() string {
	return fmt.Sprintf("service account JSON: missing or empty required field %q", e.Field)
}

func (*MissingFieldError) ExitCode() int { return 10 }

type ServiceAccount struct {
	ClientEmail string
	PrivateKey  string
	TokenURI    string
	ProjectID   string

	Type         string
	PrivateKeyID string

	Raw []byte
}

func Load(path string) (*ServiceAccount, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func LoadFromFS(_ context.Context, fr FileReader, path string) (*ServiceAccount, error) {
	data, err := fr.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func Parse(data []byte) (*ServiceAccount, error) {
	var raw struct {
		Type         string `json:"type"`
		PrivateKeyID string `json:"private_key_id"`
		ClientEmail  string `json:"client_email"`
		PrivateKey   string `json:"private_key"`
		TokenURI     string `json:"token_uri"`
		ProjectID    string `json:"project_id"`
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
		ClientEmail:  raw.ClientEmail,
		PrivateKey:   raw.PrivateKey,
		TokenURI:     raw.TokenURI,
		ProjectID:    raw.ProjectID,
		Type:         raw.Type,
		PrivateKeyID: raw.PrivateKeyID,
		Raw:          data,
	}, nil
}
