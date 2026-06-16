// Package orchestrator — UploadMapping owns the standalone
// mapping-upload choreography behind `gplay releases mappings upload`:
// open an Edit, POST the deobfuscation file keyed by an explicit
// versionCode, and commit. Unlike Upload it touches no track and uploads
// no AAB — it attaches a ProGuard/R8 mapping (a Mapping, per CONTEXT.md)
// to an already-published version after the fact, so Play vitals can
// symbolicate that version's obfuscated crash stacks (#250).
package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/mappings"
)

// MappingOpts is the input contract for UploadMapping. FileType empty
// defaults to proguard (the mapping.txt common case).
type MappingOpts struct {
	Package     string
	VersionCode int
	MappingPath string
	// FileType is the deobfuscation file type, taken verbatim from the
	// Discovery enum (proguard / nativeCode). Empty defaults to proguard.
	FileType string

	KeepEditOnFailure bool

	// DryRun validates inputs and the mapping is readable without any
	// HTTP: no Edit is opened, nothing is uploaded.
	DryRun bool
}

// MappingResult is what UploadMapping returns on success. Raw carries the
// verbatim deobfuscationfiles.upload body for the ADR-0003 pass-through.
type MappingResult struct {
	VersionCode int             `json:"versionCode"`
	FileType    string          `json:"deobfuscationFileType"`
	SymbolType  string          `json:"symbolType,omitempty"`
	Raw         json.RawMessage `json:"-"`
}

// resolveFileType applies the proguard default and rejects any value
// outside the Discovery enum. Centralized so the live and dry-run paths
// validate identically (every misuse → *InvalidOptsError, exit 2).
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

// validateMappingOpts enforces the caller-side preconditions shared by
// the live and dry-run paths: a positive versionCode and an in-enum file
// type. Every failure maps to *InvalidOptsError (exit 2).
func validateMappingOpts(opts MappingOpts) (string, error) {
	if opts.VersionCode <= 0 {
		return "", &InvalidOptsError{Message: "VersionCode must be a positive APK version code"}
	}
	return resolveFileType(opts.FileType)
}

// UploadMapping uploads a deobfuscation file for an existing versionCode
// inside its own Edit: edits.insert → deobfuscationfiles.upload →
// edits.commit. On any failure the Edit is auto-discarded (unless
// KeepEditOnFailure). opts.DryRun validates inputs and the mapping is
// readable without any HTTP.
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
		// Parity with the live path (mappings.Upload): a directory passes
		// Stat but cannot be streamed, so reject non-regular files in
		// dry-run too, keeping --dry-run and live behavior aligned.
		if !st.Mode().IsRegular() {
			return nil, &dryRunError{msg: "mapping is not a regular file: " + opts.MappingPath}
		}
		return &MappingResult{VersionCode: opts.VersionCode, FileType: fileType}, nil
	}

	result := &MappingResult{VersionCode: opts.VersionCode, FileType: fileType}
	err = edits.WithEdit(ctx, hc, opts.Package, edits.Options{KeepOnFailure: opts.KeepEditOnFailure}, func(editID string) error {
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
