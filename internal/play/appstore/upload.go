package appstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opUploadAPK    = "appstoreappsreview.uploadApk"
	opUploadImage  = "appstoreappsreview.uploadImage"
	opUploadPolicy = "appstoreappsreview.uploadAppStoreAppPolicyDeclarationFile"
)

const DeclarationFileTypeDocument = "DECLARATION_FILE_TYPE_DOCUMENT"

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

func UploadAPK(ctx context.Context, hc *http.Client, storePackage, pkg, path string) (string, json.RawMessage, error) {
	raw, err := uploadMedia(ctx, hc, mUploadAPK, opUploadAPK, storePackage, pkg, path, "application/octet-stream", nil)
	if err != nil {
		return "", nil, err
	}
	return trackingID(opUploadAPK, pkg, raw, "apkId")
}

func UploadImage(ctx context.Context, hc *http.Client, storePackage, pkg, path string) (string, json.RawMessage, error) {
	raw, err := uploadMedia(ctx, hc, mUploadImage, opUploadImage, storePackage, pkg, path, "", nil)
	if err != nil {
		return "", nil, err
	}
	return trackingID(opUploadImage, pkg, raw, "imageId")
}

func UploadPolicyDeclarationFile(ctx context.Context, hc *http.Client, storePackage, pkg, path string) (string, json.RawMessage, error) {
	initiate, err := json.Marshal(struct {
		FileType string `json:"fileType"`
	}{FileType: DeclarationFileTypeDocument})
	if err != nil {
		return "", nil, api.BuildFailure(api.Error{Operation: opUploadPolicy, Package: pkg, Message: "marshal request: " + err.Error(), Cause: err})
	}
	raw, err := uploadMedia(ctx, hc, mUploadPolicy, opUploadPolicy, storePackage, pkg, path, "", initiate)
	if err != nil {
		return "", nil, err
	}
	return trackingID(opUploadPolicy, pkg, raw, "fileId")
}

func uploadMedia(ctx context.Context, hc *http.Client, m apiregistry.Method, op, storePackage, pkg, path, contentType string, initiate []byte) (json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &LocalIOError{Op: op, Path: path, Cause: err}
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, &LocalIOError{Op: op, Path: path, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return nil, &LocalIOError{Op: op, Path: path, Cause: fmt.Errorf("not a regular file")}
	}

	if contentType == "" {
		contentType, err = sniffContentType(f, info.Size())
		if err != nil {
			return nil, &LocalIOError{Op: op, Path: path, Cause: err}
		}
	}

	u, err := m.UploadURL(map[string]string{
		"appStorePackageName": storePackage,
		"packageName":         pkg,
	})
	if err != nil {
		return nil, api.BuildFailure(api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err})
	}
	u += "?uploadType=resumable"

	if initiate != nil {
		raw, _, err := api.ResumableUploadWithInitiateBody(
			ctx, hc, op, api.PackageResource(pkg), u, contentType, f, info.Size(),
			initiate, jsonUTF8,
		)
		return raw, err
	}
	raw, _, err := api.ResumableUpload(ctx, hc, op, pkg, u, contentType, f, info.Size())
	return raw, err
}

func sniffContentType(f *os.File, size int64) (string, error) {
	n := int64(512)
	if size < n {
		n = size
	}
	if n == 0 {
		return "", fmt.Errorf("file is empty")
	}
	head := make([]byte, n)
	if _, err := f.ReadAt(head, 0); err != nil {
		return "", err
	}
	return http.DetectContentType(head), nil
}

func trackingID(op, pkg string, raw json.RawMessage, field string) (string, json.RawMessage, error) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", nil, api.DecodeFailure(api.Error{Operation: op, Package: pkg, Message: "decode response: " + err.Error(), Cause: err})
	}
	var id string
	if v, ok := parsed[field]; ok {
		if err := json.Unmarshal(v, &id); err != nil {
			return "", nil, api.DecodeFailure(api.Error{Operation: op, Package: pkg, Message: "decode response: " + field + ": " + err.Error(), Cause: err})
		}
	}
	if id == "" {
		return "", nil, api.DecodeFailure(api.Error{Operation: op, Package: pkg, Message: "upload succeeded but the response carried no " + field + ": the id is required by `gplay appstore submit`"})
	}
	return id, raw, nil
}
