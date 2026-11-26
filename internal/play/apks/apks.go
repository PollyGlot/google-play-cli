package apks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const op = "apks.upload"

var method = apiregistry.MustResolve("androidpublisher.edits.apks.upload")

type LocalIOError struct {
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: %s: %v", op, e.Path, e.Cause)
}

func (e *LocalIOError) Unwrap() error { return e.Cause }

func (e *LocalIOError) ExitCode() int { return 20 }

func Upload(ctx context.Context, hc *http.Client, pkg, editID, apkPath string) (int, error) {
	f, err := os.Open(apkPath)
	if err != nil {
		return 0, &LocalIOError{Path: apkPath, Cause: err}
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return 0, &LocalIOError{Path: apkPath, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return 0, &LocalIOError{Path: apkPath, Cause: fmt.Errorf("not a regular file")}
	}

	u, err := method.UploadURL(map[string]string{"packageName": pkg, "editId": editID})
	if err != nil {
		return 0, api.BuildFailure(api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err})
	}

	body, status, err := api.ResumableUpload(ctx, hc, op, pkg, u, "application/octet-stream", f, info.Size())
	if err != nil {
		return 0, err
	}
	var parsed struct {
		VersionCode int `json:"versionCode"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, api.DecodeFailure(api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: status,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		})
	}
	return parsed.VersionCode, nil
}
