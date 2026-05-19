package resolver_test

import (
	"errors"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

const fakeSAJSON = `{
  "type": "service_account",
  "project_id": "p",
  "private_key": "-----BEGIN PRIVATE KEY-----\nXX\n-----END PRIVATE KEY-----\n",
  "client_email": "ci@p.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

func newResolver(t *testing.T) (*resolver.Resolver, *config.Config, keystore.Backend) {
	t.Helper()
	cfg := &config.Config{}
	be := keystore.NewFileBackend(t.TempDir())
	return resolver.New(cfg, be), cfg, be
}

func TestResolve_returnsActiveAccountCredential(t *testing.T) {
	r, cfg, be := newResolver(t)

	cfg.AddAccount("ci")
	if err := cfg.SetActive("ci"); err != nil {
		t.Fatal(err)
	}
	if err := be.Save("ci", []byte(fakeSAJSON)); err != nil {
		t.Fatal(err)
	}

	sa, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolve_noActiveAccount_returnsErrNoActive(t *testing.T) {
	r, _, _ := newResolver(t)
	_, err := r.Resolve()
	if !errors.Is(err, resolver.ErrNoActive) {
		t.Errorf("Resolve (empty config) = %v, want ErrNoActive", err)
	}
}
