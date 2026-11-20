package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/apks"
	"github.com/PollyGlot/google-play-cli/internal/play/bundles"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/mappings"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
)

func confirmRequired(status string) error {
	return exit.SafetyFlag("confirm", "production publish (status=%s) requires --confirm to prevent accidental rollouts", status)
}

type InvalidOptsError struct {
	Message string
}

func (e *InvalidOptsError) Error() string { return e.Message }
func (e *InvalidOptsError) ExitCode() int { return 2 }

const MaxUpdatePriority = 5

func validateUpdatePriority(p *int) error {
	if p != nil && (*p < 0 || *p > MaxUpdatePriority) {
		return &InvalidOptsError{Message: "UpdatePriority must be in 0..5"}
	}
	return nil
}

func requiresConfirm(track string, state ReleaseState) bool {
	return transitionReachesProdUsers(track, state.resolve(track).Status())
}

type Opts struct {
	Package            string
	Track              string
	Artifacts          []Artifact
	RetainVersionCodes []int

	State             ReleaseState
	UpdatePriority    *int
	ReleaseName       string
	ReleaseNotes      string
	ReleaseNotesDir   string
	KeepEditOnFailure bool

	ExplicitEditID string

	Commit edits.CommitOptions

	MappingPath string

	DeviceTierConfig string

	Confirm bool

	DryRun bool
}

type Result struct {
	VersionCode         int             `json:"versionCode"`
	Track               string          `json:"track"`
	ReleaseName         string          `json:"releaseName"`
	Status              string          `json:"status"`
	UserFraction        float64         `json:"userFraction,omitempty"`
	InAppUpdatePriority *int            `json:"inAppUpdatePriority,omitempty"`
	DefaultLanguage     string          `json:"defaultLanguage,omitempty"`
	Locales             []string        `json:"locales,omitempty"`
	RawTrackResponse    json.RawMessage `json:"-"`

	MappingUploaded bool `json:"-"`

	UploadedVersionCodes []int `json:"-"`
	RetainedVersionCodes []int `json:"-"`

	SupersededVersionCodes []int `json:"-"`

	ReplacedVersionCodes []int `json:"-"`

	Requires []string `json:"-"`

	Plan *TransitionPlan `json:"-"`

	Promotion *PromotePlan `json:"-"`
}

func safetyFlags(reachesProdUsers bool) []string {
	if reachesProdUsers {
		return []string{"confirm"}
	}
	return nil
}

type Artifact struct {
	Path   string
	Format string
}

func Upload(ctx context.Context, hc *http.Client, opts Opts) (*Result, error) {
	if err := validateOpts(opts); err != nil {
		return nil, err
	}
	hosted, err := loadHosted(opts)
	if err != nil {
		return nil, err
	}

	if opts.DryRun {
		return dryRunResult(opts)
	}

	if requiresConfirm(opts.Track, opts.State) && !opts.Confirm {
		return nil, confirmRequired(opts.State.resolve(opts.Track).Status())
	}

	if err := notes.Precheck(opts.ReleaseNotes, opts.ReleaseNotesDir); err != nil {
		return nil, err
	}

	result := &Result{Track: opts.Track}

	err = edits.WithEdit(ctx, hc, opts.Package, edits.Options{KeepOnFailure: opts.KeepEditOnFailure, ExplicitEditID: opts.ExplicitEditID, Commit: opts.Commit}, func(editID string) error {
		var localized []tracks.LocalizedText
		if opts.ReleaseNotes != "" || opts.ReleaseNotesDir != "" {
			needLang := opts.ReleaseNotes != "" || hasDefaultTxt(opts.ReleaseNotesDir)
			loadOpts := notes.Opts{
				Text: opts.ReleaseNotes,
				Dir:  opts.ReleaseNotesDir,
			}
			if needLang {
				lang, err := details.GetDefaultLanguage(ctx, hc, opts.Package, editID)
				if err != nil {
					return err
				}
				result.DefaultLanguage = lang
				loadOpts.DefaultLanguage = lang
			}
			loaded, err := notes.Load(loadOpts)
			if err != nil {
				return err
			}
			localized = make([]tracks.LocalizedText, 0, len(loaded))
			for _, n := range loaded {
				localized = append(localized, tracks.LocalizedText{Language: n.Locale, Text: n.Text})
				result.Locales = append(result.Locales, n.Locale)
			}
		}

		codes := make([]int, 0, len(opts.Artifacts))
		for i, a := range opts.Artifacts {
			code, err := uploadArtifact(ctx, hc, opts, editID, a, hosted[i])
			if err != nil {
				return err
			}
			codes = append(codes, code)
		}
		result.VersionCode = highest(codes)
		result.UploadedVersionCodes = codes
		result.RetainedVersionCodes = opts.RetainVersionCodes

		if opts.MappingPath != "" {
			if _, err := mappings.Upload(ctx, hc, opts.Package, editID, codes[0], mappings.TypeProguard, opts.MappingPath); err != nil {
				return err
			}
			result.MappingUploaded = true
		}

		release := buildRelease(codes, opts)
		release.ReleaseNotes = localized
		_, raw, err := tracks.Update(ctx, hc, opts.Package, editID, opts.Track, release)
		if err != nil {
			return err
		}
		result.ReleaseName = release.Name
		result.Status = release.Status
		result.UserFraction = release.UserFraction
		result.InAppUpdatePriority = release.InAppUpdatePriority
		result.RawTrackResponse = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func uploadArtifact(ctx context.Context, hc *http.Client, opts Opts, editID string, a Artifact, hosted json.RawMessage) (int, error) {
	switch a.Format {
	case FormatAPK:
		return apks.Upload(ctx, hc, opts.Package, editID, a.Path)
	case FormatExternallyHosted:
		code, _, err := apks.AddExternallyHosted(ctx, hc, opts.Package, editID, hosted)
		return code, err
	}
	return bundles.UploadWith(ctx, hc, opts.Package, editID, a.Path,
		bundles.Options{DeviceTierConfigID: opts.DeviceTierConfig})
}

func highest(codes []int) int {
	top := 0
	for _, c := range codes {
		top = max(top, c)
	}
	return top
}

func validateOpts(opts Opts) error {
	if len(opts.Artifacts) == 0 {
		return &InvalidOptsError{Message: "Artifacts must name at least one file to upload"}
	}
	if opts.MappingPath != "" && len(opts.Artifacts) > 1 {
		return &InvalidOptsError{Message: "MappingPath applies to a single artifact, got " + strconv.Itoa(len(opts.Artifacts))}
	}
	if err := validateUpdatePriority(opts.UpdatePriority); err != nil {
		return err
	}
	if opts.ReleaseName != "" && strings.TrimSpace(opts.ReleaseName) == "" {
		return &InvalidOptsError{Message: "ReleaseName must not be blank"}
	}
	if opts.ReleaseNotes != "" && opts.ReleaseNotesDir != "" {
		return &InvalidOptsError{
			Message: "ReleaseNotes and ReleaseNotesDir are mutually exclusive: pick one",
		}
	}
	if err := notes.ValidateDirLocales(opts.ReleaseNotesDir); err != nil {
		return err
	}
	return nil
}

func hasDefaultTxt(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "default.txt")); err == nil {
		return true
	}
	return false
}

func buildRelease(uploaded []int, opts Opts) tracks.Release {
	state := opts.State.resolve(opts.Track)
	name := strconv.Itoa(highest(uploaded))
	if opts.ReleaseName != "" {
		name = opts.ReleaseName
	}
	codes := make([]string, 0, len(uploaded)+len(opts.RetainVersionCodes))
	for _, c := range uploaded {
		codes = append(codes, strconv.Itoa(c))
	}
	for _, c := range opts.RetainVersionCodes {
		codes = append(codes, strconv.Itoa(c))
	}
	return tracks.Release{
		Name:                name,
		Status:              state.Status(),
		UserFraction:        state.UserFraction(),
		VersionCodes:        codes,
		InAppUpdatePriority: opts.UpdatePriority,
	}
}

const TrackProduction = "production"

const (
	FormatBundle           = "bundle"
	FormatAPK              = "apk"
	FormatExternallyHosted = "externallyHosted"
)

type HostedError struct {
	Path string
	Msg  string
}

func (e *HostedError) Error() string { return "externally hosted APK " + e.Path + ": " + e.Msg }
func (e *HostedError) ExitCode() int { return 20 }

func LoadExternallyHosted(path, pkg string) (json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &HostedError{Path: path, Msg: err.Error()}
	}
	if !json.Valid(data) {
		return nil, &HostedError{Path: path, Msg: "not valid JSON (an ExternallyHostedApk resource; see gplay schema ExternallyHostedApk)"}
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil || probe == nil {
		return nil, &HostedError{Path: path, Msg: "not a JSON object (an ExternallyHostedApk resource; see gplay schema ExternallyHostedApk)"}
	}
	if raw, ok := probe["packageName"]; ok {
		var declared string
		if err := json.Unmarshal(raw, &declared); err != nil {
			return nil, &HostedError{Path: path, Msg: "packageName is not a string"}
		}
		if declared != pkg {
			return nil, &HostedError{Path: path, Msg: "packageName is " + strconv.Quote(declared) + " but the package being released is " + strconv.Quote(pkg)}
		}
	}
	return json.RawMessage(data), nil
}

func loadHosted(opts Opts) ([]json.RawMessage, error) {
	hosted := make([]json.RawMessage, len(opts.Artifacts))
	for i, a := range opts.Artifacts {
		if a.Format != FormatExternallyHosted {
			continue
		}
		raw, err := LoadExternallyHosted(a.Path, opts.Package)
		if err != nil {
			return nil, err
		}
		hosted[i] = raw
	}
	return hosted, nil
}

func dryRunResult(opts Opts) (*Result, error) {
	for _, a := range opts.Artifacts {
		st, err := os.Stat(a.Path)
		if err != nil {
			return nil, &dryRunError{msg: "artifact not accessible: " + err.Error()}
		}
		if !st.Mode().IsRegular() {
			return nil, &dryRunError{msg: "artifact is not a regular file: " + a.Path}
		}
	}

	if opts.MappingPath != "" {
		st, err := os.Stat(opts.MappingPath)
		if err != nil {
			return nil, &dryRunError{msg: "mapping not accessible: " + err.Error()}
		}
		if !st.Mode().IsRegular() {
			return nil, &dryRunError{msg: "mapping is not a regular file: " + opts.MappingPath}
		}
	}

	if opts.ReleaseNotes != "" {
		if err := notes.Precheck(opts.ReleaseNotes, ""); err != nil {
			return nil, &dryRunError{msg: err.Error()}
		}
	}

	var previewLocales []string
	if opts.ReleaseNotesDir != "" {
		st, err := os.Stat(opts.ReleaseNotesDir)
		if err != nil {
			return nil, &dryRunError{msg: "release-notes-dir not accessible: " + err.Error()}
		}
		if !st.IsDir() {
			return nil, &dryRunError{msg: "release-notes-dir is not a directory: " + opts.ReleaseNotesDir}
		}
		loaded, err := notes.Load(notes.Opts{
			Dir:             opts.ReleaseNotesDir,
			DefaultLanguage: notes.OfflineDefaultLanguage,
		})
		if err != nil {
			return nil, &dryRunError{msg: err.Error()}
		}
		for _, n := range loaded {
			if n.Locale == notes.OfflineDefaultLanguage {
				previewLocales = append(previewLocales, "(default-language)")
			} else {
				previewLocales = append(previewLocales, n.Locale)
			}
		}
	}

	release := buildRelease([]int{0}, opts)
	return &Result{
		Track:                opts.Track,
		VersionCode:          0,
		Status:               release.Status,
		UserFraction:         release.UserFraction,
		InAppUpdatePriority:  release.InAppUpdatePriority,
		ReleaseName:          dryRunReleaseName(opts),
		Locales:              previewLocales,
		RetainedVersionCodes: opts.RetainVersionCodes,
	}, nil
}

type dryRunError struct{ msg string }

func (e *dryRunError) Error() string { return "dry-run: " + e.msg }
func (e *dryRunError) ExitCode() int { return 20 }

func dryRunReleaseName(opts Opts) string {
	if opts.ReleaseName != "" {
		return opts.ReleaseName
	}
	return "(dry-run)"
}
