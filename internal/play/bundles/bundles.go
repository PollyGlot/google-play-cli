package bundles

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const op = "bundles.upload"

var method = apiregistry.MustResolve("androidpublisher.edits.bundles.upload")

type LocalIOError struct {
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: %s: %v", op, e.Path, e.Cause)
}

func (e *LocalIOError) Unwrap() error { return e.Cause }

func (e *LocalIOError) ExitCode() int { return 20 }

func Upload(ctx context.Context, hc *http.Client, pkg, editID, aabPath string) (int, error) {
	return UploadWith(ctx, hc, pkg, editID, aabPath, Options{})
}

type Options struct {
	DeviceTierConfigID string
}

func UploadWith(ctx context.Context, hc *http.Client, pkg, editID, aabPath string, o Options) (int, error) {
	f, err := os.Open(aabPath)
	if err != nil {
		return 0, &LocalIOError{Path: aabPath, Cause: err}
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return 0, &LocalIOError{Path: aabPath, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return 0, &LocalIOError{Path: aabPath, Cause: fmt.Errorf("not a regular file")}
	}

	u, err := method.UploadURL(map[string]string{"packageName": pkg, "editId": editID})
	if err != nil {
		return 0, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	q := url.Values{"uploadType": {"resumable"}}
	if o.DeviceTierConfigID != "" {
		q.Set("deviceTierConfigId", o.DeviceTierConfigID)
	}
	u += "?" + q.Encode()

	body, status, err := api.ResumableUpload(ctx, hc, op, pkg, u, "application/octet-stream", f, info.Size())
	if err != nil {
		return 0, err
	}

	var parsed struct {
		VersionCode int `json:"versionCode"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, &api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: status,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return parsed.VersionCode, nil
}
