package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
)

const AndroidPublisherScope = "https://www.googleapis.com/auth/androidpublisher"

const ReportingScope = "https://www.googleapis.com/auth/playdeveloperreporting"

const StorageReadOnlyScope = "https://www.googleapis.com/auth/devstorage.read_only"

const serviceAccountType = "service_account"

type AuthError struct {
	StatusCode int
	Body       string
	Cause      error
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("token: oauth2 exchange failed (%d): %s", e.StatusCode, e.Body)
}

func (e *AuthError) Unwrap() error { return e.Cause }

func (*AuthError) ExitCode() int { return 10 }

func Source(ctx context.Context, sa *serviceaccount.ServiceAccount, scopes ...string) (oauth2.TokenSource, error) {
	if len(scopes) == 0 {
		scopes = []string{AndroidPublisherScope}
	}
	if sa.Type != serviceAccountType {
		return nil, fmt.Errorf("token: credentials JSON 'type' field is %q (expected %q)", sa.Type, serviceAccountType)
	}
	cfg := &jwt.Config{
		Email:        sa.ClientEmail,
		PrivateKey:   []byte(sa.PrivateKey),
		PrivateKeyID: sa.PrivateKeyID,
		Scopes:       append([]string(nil), scopes...),
		TokenURL:     sa.TokenURI,
	}
	return &wrappedSource{inner: cfg.TokenSource(ctx)}, nil
}

type wrappedSource struct {
	inner oauth2.TokenSource
}

func (w *wrappedSource) Token() (*oauth2.Token, error) {
	tok, err := w.inner.Token()
	if err == nil {
		return tok, nil
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.Response != nil && isAuthRefusal(re) {
			return nil, &AuthError{
				StatusCode: re.Response.StatusCode,
				Body:       string(re.Body),
				Cause:      err,
			}
		}
	}
	return nil, err
}

var credentialRefusals = map[string]bool{
	"invalid_grant":       true,
	"unauthorized_client": true,
	"invalid_client":      true,
}

func isAuthRefusal(re *oauth2.RetrieveError) bool {
	switch re.Response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	case http.StatusBadRequest:
		return credentialRefusals[retrieveErrorCode(re)]
	}
	return false
}

func retrieveErrorCode(re *oauth2.RetrieveError) string {
	if re.ErrorCode != "" {
		return re.ErrorCode
	}
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(re.Body, &body) != nil {
		return ""
	}
	return body.Error
}
