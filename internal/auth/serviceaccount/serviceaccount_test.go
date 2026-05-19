package serviceaccount_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
)

const validSAJSON = `{
  "type": "service_account",
  "project_id": "test-proj",
  "private_key_id": "abc123",
  "private_key": "-----BEGIN PRIVATE KEY-----\nMIIBCG\n-----END PRIVATE KEY-----\n",
  "client_email": "ci@test-proj.iam.gserviceaccount.com",
  "client_id": "0123456789",
  "auth_uri": "https://accounts.google.com/o/oauth2/auth",
  "token_uri": "https://oauth2.googleapis.com/token",
  "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
  "client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/ci%40test-proj.iam.gserviceaccount.com"
}`

func writeTempJSON(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sa.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write tmp sa: %v", err)
	}
	return path
}

func TestLoad_validJSON_returnsExpectedFields(t *testing.T) {
	path := writeTempJSON(t, validSAJSON)

	sa, err := serviceaccount.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := sa.ClientEmail, "ci@test-proj.iam.gserviceaccount.com"; got != want {
		t.Errorf("ClientEmail = %q, want %q", got, want)
	}
	if got, want := sa.ProjectID, "test-proj"; got != want {
		t.Errorf("ProjectID = %q, want %q", got, want)
	}
	if got, want := sa.TokenURI, "https://oauth2.googleapis.com/token"; got != want {
		t.Errorf("TokenURI = %q, want %q", got, want)
	}
	if !strings.Contains(sa.PrivateKey, "BEGIN PRIVATE KEY") {
		t.Errorf("PrivateKey missing PEM header, got %q", sa.PrivateKey)
	}
}

func TestLoad_malformedJSON_returnsParseError(t *testing.T) {
	path := writeTempJSON(t, "{not valid json")
	_, err := serviceaccount.Load(path)
	if err == nil {
		t.Fatal("Load: expected parse error on malformed JSON")
	}
	var fe *serviceaccount.MissingFieldError
	if errors.As(err, &fe) {
		t.Fatalf("Load: malformed JSON should not surface as MissingFieldError, got %v", err)
	}
}

func TestLoad_unknownFields_tolerated(t *testing.T) {
	body := strings.Replace(
		validSAJSON,
		`"type": "service_account",`,
		`"type": "service_account", "future_field": "ignored", "another": 42,`,
		1,
	)
	path := writeTempJSON(t, body)
	sa, err := serviceaccount.Load(path)
	if err != nil {
		t.Fatalf("Load: unknown fields should be tolerated, got %v", err)
	}
	if sa.ClientEmail == "" {
		t.Errorf("ClientEmail not parsed despite unknown fields present")
	}
}

func TestLoad_emptyFile_returnsError(t *testing.T) {
	path := writeTempJSON(t, "")
	_, err := serviceaccount.Load(path)
	if err == nil {
		t.Fatal("Load: expected error for empty file")
	}
}

func TestLoad_missingRequiredField_returnsTypedFieldError(t *testing.T) {
	cases := []struct {
		name      string
		blankWhat string
		want      string
	}{
		{"client_email", `"ci@test-proj.iam.gserviceaccount.com"`, "client_email"},
		{"private_key", `"-----BEGIN PRIVATE KEY-----\nMIIBCG\n-----END PRIVATE KEY-----\n"`, "private_key"},
		{"token_uri", `"https://oauth2.googleapis.com/token"`, "token_uri"},
		{"project_id", `"test-proj"`, "project_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(validSAJSON, tc.blankWhat, `""`, 1)
			path := writeTempJSON(t, body)

			_, err := serviceaccount.Load(path)
			if err == nil {
				t.Fatalf("Load: expected error for missing %s, got nil", tc.name)
			}
			var fe *serviceaccount.MissingFieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Load: got %T (%v), want *MissingFieldError", err, err)
			}
			if fe.Field != tc.want {
				t.Errorf("MissingFieldError.Field = %q, want %q", fe.Field, tc.want)
			}
		})
	}
}
