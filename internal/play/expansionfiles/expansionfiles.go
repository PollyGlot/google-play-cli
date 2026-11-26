package expansionfiles

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opUpload = "expansionfiles.upload"
	opUpdate = "expansionfiles.update"
	opGet    = "expansionfiles.get"
)

var (
	methodUpload = apiregistry.MustResolve("androidpublisher.edits.expansionfiles.upload")
	methodUpdate = apiregistry.MustResolve("androidpublisher.edits.expansionfiles.update")
	methodGet    = apiregistry.MustResolve("androidpublisher.edits.expansionfiles.get")
)

const (
	TypeMain  = "main"
	TypePatch = "patch"
)

type ExpansionFile struct {
	FileSize          string `json:"fileSize,omitempty"`
	ReferencesVersion int    `json:"referencesVersion,omitempty"`
}

func (e ExpansionFile) HasFile() bool {
	return e.FileSize != "" && e.FileSize != "0"
}

type LocalIOError struct {
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string { return fmt.Sprintf("%s: %s: %v", opUpload, e.Path, e.Cause) }
func (e *LocalIOError) Unwrap() error { return e.Cause }
func (e *LocalIOError) ExitCode() int { return 20 }

func fileParams(pkg, editID string, versionCode int, fileType string) map[string]string {
	return map[string]string{
		"packageName":       pkg,
		"editId":            editID,
		"apkVersionCode":    strconv.Itoa(versionCode),
		"expansionFileType": fileType,
	}
}

func Upload(ctx context.Context, hc *http.Client, pkg, editID string, versionCode int, fileType, path string) (json.RawMessage, error) {
	f, info, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	u, err := methodUpload.UploadURL(fileParams(pkg, editID, versionCode, fileType))
	if err != nil {
		return nil, api.BuildFailure(api.Error{Operation: opUpload, Package: pkg, Message: err.Error(), Cause: err})
	}

	body, _, err := api.ResumableUpload(ctx, hc, opUpload, pkg, u, "application/octet-stream", f, info.Size())
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

func Update(ctx context.Context, hc *http.Client, pkg, editID string, versionCode int, fileType string, referencesVersion int) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: methodUpdate, Op: opUpdate, Target: pkg,
		Params: fileParams(pkg, editID, versionCode, fileType),
		Body:   ExpansionFile{ReferencesVersion: referencesVersion},
	})
}

func Get(ctx context.Context, hc *http.Client, pkg, editID string, versionCode int, fileType string) (ExpansionFile, json.RawMessage, error) {
	var ef ExpansionFile
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: methodGet, Op: opGet, Target: pkg,
		Params: fileParams(pkg, editID, versionCode, fileType),
	}, &ef)
	if err != nil {
		return ExpansionFile{}, nil, err
	}
	return ef, raw, nil
}

func openRegular(path string) (*os.File, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, &LocalIOError{Path: path, Cause: err}
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, &LocalIOError{Path: path, Cause: err}
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, &LocalIOError{Path: path, Cause: fmt.Errorf("not a regular file")}
	}
	return f, info, nil
}
