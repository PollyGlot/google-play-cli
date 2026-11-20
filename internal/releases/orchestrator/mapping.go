package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/mappings"
)

type MappingOpts struct {
	Package     string
	VersionCode int
	MappingPath string
	FileType    string

	KeepEditOnFailure bool

	ExplicitEditID string

	Commit edits.CommitOptions

	DryRun bool
}

type MappingResult struct {
	VersionCode int             `json:"versionCode"`
	FileType    string          `json:"deobfuscationFileType"`
	SymbolType  string          `json:"symbolType,omitempty"`
	Raw         json.RawMessage `json:"-"`
}

func resolveFileType(fileType string) (string, error) {
	switch fileType {
	case "":
		return mappings.TypeProguard, nil
	case mappings.TypeProguard, mappings.TypeNativeCode:
		return fileType, nil
	}
	return "", &InvalidOptsError{
		Message: "FileType must be " + mappings.TypeProguard + " or " + mappings.TypeNativeCode,
	}
}

func validateMappingOpts(opts MappingOpts) (string, error) {
	if opts.VersionCode <= 0 {
		return "", &InvalidOptsError{Message: "VersionCode must be a positive APK version code"}
	}
	return resolveFileType(opts.FileType)
}

func UploadMapping(ctx context.Context, hc *http.Client, opts MappingOpts) (*MappingResult, error) {
	fileType, err := validateMappingOpts(opts)
	if err != nil {
		return nil, err
	}

	if opts.DryRun {
		st, err := os.Stat(opts.MappingPath)
		if err != nil {
			return nil, &dryRunError{msg: "mapping not accessible: " + err.Error()}
		}
		if !st.Mode().IsRegular() {
			return nil, &dryRunError{msg: "mapping is not a regular file: " + opts.MappingPath}
		}
		return &MappingResult{VersionCode: opts.VersionCode, FileType: fileType}, nil
	}

	result := &MappingResult{VersionCode: opts.VersionCode, FileType: fileType}
	err = edits.WithEdit(ctx, hc, opts.Package, edits.Options{KeepOnFailure: opts.KeepEditOnFailure, ExplicitEditID: opts.ExplicitEditID, Commit: opts.Commit}, func(editID string) error {
		res, err := mappings.Upload(ctx, hc, opts.Package, editID, opts.VersionCode, fileType, opts.MappingPath)
		if err != nil {
			return err
		}
		result.SymbolType = res.SymbolType
		result.Raw = res.Raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
