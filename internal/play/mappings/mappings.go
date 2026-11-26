package mappings

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

const op = "deobfuscationfiles.upload"

var method = apiregistry.MustResolve("androidpublisher.edits.deobfuscationfiles.upload")

const (
	TypeProguard   = "proguard"
	TypeNativeCode = "nativeCode"
)

type Result struct {
	SymbolType string
	Raw        json.RawMessage
}

type LocalIOError struct {
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: %s: %v", op, e.Path, e.Cause)
}

func (e *LocalIOError) Unwrap() error { return e.Cause }

func (e *LocalIOError) ExitCode() int { return 20 }

func Upload(ctx context.Context, hc *http.Client, pkg, editID string, versionCode int, fileType, path string) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &LocalIOError{Path: path, Cause: err}
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, &LocalIOError{Path: path, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return nil, &LocalIOError{Path: path, Cause: fmt.Errorf("not a regular file")}
	}

	u, err := method.UploadURL(map[string]string{
		"packageName":           pkg,
		"editId":                editID,
		"apkVersionCode":        strconv.Itoa(versionCode),
		"deobfuscationFileType": fileType,
	})
	if err != nil {
		return nil, api.BuildFailure(api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err})
	}
	u += "?uploadType=resumable"

	body, status, err := api.ResumableUpload(ctx, hc, op, pkg, u, "application/octet-stream", f, info.Size())
	if err != nil {
		return nil, err
	}
	var parsed struct {
		DeobfuscationFile struct {
			SymbolType string `json:"symbolType"`
		} `json:"deobfuscationFile"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, api.DecodeFailure(api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: status,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		})
	}
	return &Result{SymbolType: parsed.DeobfuscationFile.SymbolType, Raw: body}, nil
}
