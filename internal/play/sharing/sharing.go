package sharing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opUploadAPK    = "internalappsharingartifacts.uploadapk"
	opUploadBundle = "internalappsharingartifacts.uploadbundle"
)

var (
	mUploadAPK    = apiregistry.MustResolve("androidpublisher.internalappsharingartifacts.uploadapk")
	mUploadBundle = apiregistry.MustResolve("androidpublisher.internalappsharingartifacts.uploadbundle")
)

type Artifact struct {
	DownloadURL            string `json:"downloadUrl,omitempty"`
	CertificateFingerprint string `json:"certificateFingerprint,omitempty"`
	SHA256                 string `json:"sha256,omitempty"`
}

type LocalIOError struct {
	Op    string
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: %s: %v", e.Op, e.Path, e.Cause)
}
func (e *LocalIOError) Unwrap() error { return e.Cause }
func (e *LocalIOError) ExitCode() int { return 20 }

func UploadAPK(ctx context.Context, hc *http.Client, pkg, path string) (Artifact, json.RawMessage, error) {
	return upload(ctx, hc, mUploadAPK, opUploadAPK, pkg, path)
}

func UploadBundle(ctx context.Context, hc *http.Client, pkg, path string) (Artifact, json.RawMessage, error) {
	return upload(ctx, hc, mUploadBundle, opUploadBundle, pkg, path)
}

func upload(ctx context.Context, hc *http.Client, m apiregistry.Method, op, pkg, path string) (Artifact, json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, nil, &LocalIOError{Op: op, Path: path, Cause: err}
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return Artifact{}, nil, &LocalIOError{Op: op, Path: path, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return Artifact{}, nil, &LocalIOError{Op: op, Path: path, Cause: fmt.Errorf("not a regular file")}
	}

	raw, err := api.Do(ctx, hc, api.Call{
		Method: m, Op: op, Target: pkg,
		Params:      map[string]string{"packageName": pkg},
		Media:       true,
		Body:        &api.Stream{Open: func() (io.ReadCloser, error) { return os.Open(path) }, Size: info.Size()},
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return Artifact{}, nil, err
	}
	var art Artifact
	if err := json.Unmarshal(raw, &art); err != nil {
		return Artifact{}, nil, &api.Error{Operation: op, Package: pkg, StatusCode: http.StatusOK, Message: "decode response: " + err.Error(), Cause: err}
	}
	return art, raw, nil
}
