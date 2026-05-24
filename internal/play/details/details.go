// Package details reads app-level metadata via edits.details.get. The
// only field gplay needs today is defaultLanguage (for assigning a
// single --release-notes text or for the default.txt fallback in
// internal/releases/notes).
package details

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// GetDefaultLanguage returns the app's defaultLanguage from
// edits.details.get. Errors are wrapped in *api.Error so the gplay
// exit-code taxonomy is honored end-to-end.
func GetDefaultLanguage(ctx context.Context, hc *http.Client, pkg, editID string) (string, error) {
	u := api.AndroidPubBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/details"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return "", &api.Error{Operation: "edits.details.get", Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", &api.Error{Operation: "edits.details.get", Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		return "", &api.Error{
			Operation:  "edits.details.get",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    api.APIErrorMessage(body, resp.StatusCode),
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	var parsed struct {
		DefaultLanguage string `json:"defaultLanguage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", &api.Error{
			Operation:  "edits.details.get",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	if parsed.DefaultLanguage == "" {
		// An empty defaultLanguage with a 2xx response is an API
		// contract violation — downstream callers would emit release
		// notes with empty language keys.
		return "", &api.Error{
			Operation:  "edits.details.get",
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "missing defaultLanguage in response body",
		}
	}
	return parsed.DefaultLanguage, nil
}
