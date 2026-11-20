package teamcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	playteam "github.com/PollyGlot/google-play-cli/internal/play/team"
)

func SetUserPermissions(ctx context.Context, hc *http.Client, developerID, email string, enums []string) (json.RawMessage, error) {
	raw, err := playteam.SetUserPermissions(ctx, hc, developerID, email, enums)
	if err != nil {
		return nil, classifySetError(developerID, email, err)
	}
	return raw, nil
}

type forbiddenError struct {
	developerID string
	cause       error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not authorized to manage users on developer account %s: grant it Admin (manage-permissions) in the Play Console under Users & permissions: %v", e.developerID, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

type notMemberError struct {
	email string
	cause error
}

func (e *notMemberError) Error() string {
	return fmt.Sprintf("%s is not a member: use `gplay team users add %s` to invite them first: %v", e.email, e.email, e.cause)
}
func (e *notMemberError) Unwrap() error { return e.cause }

func classifySetError(developerID, email string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden:
			return &forbiddenError{developerID: developerID, cause: err}
		case http.StatusNotFound:
			return &notMemberError{email: email, cause: err}
		}
	}
	return err
}
