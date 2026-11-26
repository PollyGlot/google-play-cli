package tracks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opTracksGet    = "tracks.get"
	opTracksList   = "tracks.list"
	opTracksUpdate = "tracks.update"
	opTracksCreate = "tracks.create"
)

var (
	methodGet    = apiregistry.MustResolve("androidpublisher.edits.tracks.get")
	methodList   = apiregistry.MustResolve("androidpublisher.edits.tracks.list")
	methodUpdate = apiregistry.MustResolve("androidpublisher.edits.tracks.update")
	methodCreate = apiregistry.MustResolve("androidpublisher.edits.tracks.create")
)

const (
	TrackTypeClosedTesting = "CLOSED_TESTING"
	FormFactorDefault      = "DEFAULT"
	FormFactorWear         = "WEAR"
	FormFactorAutomotive   = "AUTOMOTIVE"
)

const (
	StatusDraft      = "draft"
	StatusInProgress = "inProgress"
	StatusHalted     = "halted"
	StatusCompleted  = "completed"
)

type LocalizedText struct {
	Language string `json:"language"`
	Text     string `json:"text"`
}

type Release struct {
	Name                string          `json:"name,omitempty"`
	Status              string          `json:"status,omitempty"`
	UserFraction        float64         `json:"userFraction,omitempty"`
	VersionCodes        []string        `json:"versionCodes,omitempty"`
	ReleaseNotes        []LocalizedText `json:"releaseNotes,omitempty"`
	InAppUpdatePriority *int            `json:"inAppUpdatePriority,omitempty"`
	CountryTargeting    json.RawMessage `json:"countryTargeting,omitempty"`
}

type Track struct {
	Track    string    `json:"track"`
	Releases []Release `json:"releases"`
}

type TrackConfig struct {
	Track      string `json:"track"`
	Type       string `json:"type"`
	FormFactor string `json:"formFactor"`
}

var formFactorPrefixes = map[string]string{
	FormFactorWear:       "wear:",
	FormFactorAutomotive: "automotive:",
}

func QualifiedName(name, formFactor string) (string, error) {
	for ff, prefix := range formFactorPrefixes {
		if ff != formFactor && strings.HasPrefix(name, prefix) {
			return "", fmt.Errorf("track %q carries the %s prefix %q but the form factor is %s", name, ff, prefix, formFactor)
		}
	}
	prefix := formFactorPrefixes[formFactor]
	alias := strings.TrimPrefix(name, prefix)
	if alias == "" {
		return "", fmt.Errorf("track %q has no name after its %q prefix", name, prefix)
	}
	return prefix + alias, nil
}

func Get(ctx context.Context, hc *http.Client, pkg, editID, track string) (*Track, json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodGet, Op: opTracksGet, Target: pkg,
		Params: map[string]string{"packageName": pkg, "editId": editID, "track": track},
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed Track
	if err := decode(opTracksGet, pkg, raw, &parsed); err != nil {
		return nil, raw, err
	}
	return &parsed, raw, nil
}

func List(ctx context.Context, hc *http.Client, pkg, editID string) ([]Track, json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodList, Op: opTracksList, Target: pkg,
		Params: map[string]string{"packageName": pkg, "editId": editID},
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed struct {
		Tracks []Track `json:"tracks"`
	}
	if err := decode(opTracksList, pkg, raw, &parsed); err != nil {
		return nil, raw, err
	}
	return parsed.Tracks, raw, nil
}

func Update(ctx context.Context, hc *http.Client, pkg, editID, track string, release Release) (*Track, json.RawMessage, error) {
	payload, err := json.Marshal(Track{Track: track, Releases: []Release{release}})
	if err != nil {
		return nil, nil, api.BuildFailure(api.Error{Operation: opTracksUpdate, Package: pkg, Message: "marshal payload: " + err.Error(), Cause: err})
	}
	return UpdateRaw(ctx, hc, pkg, editID, track, payload)
}

func UpdateRaw(ctx context.Context, hc *http.Client, pkg, editID, track string, body []byte) (*Track, json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodUpdate, Op: opTracksUpdate, Target: pkg,
		Params: map[string]string{"packageName": pkg, "editId": editID, "track": track},
		Body:   body,
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed Track
	if err := decode(opTracksUpdate, pkg, raw, &parsed); err != nil {
		return nil, raw, err
	}
	return &parsed, raw, nil
}

func Create(ctx context.Context, hc *http.Client, pkg, editID, name, formFactor string) (*Track, json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodCreate, Op: opTracksCreate, Target: pkg,
		Params: map[string]string{"packageName": pkg, "editId": editID},
		Body:   TrackConfig{Track: name, Type: TrackTypeClosedTesting, FormFactor: formFactor},
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed Track
	if err := decode(opTracksCreate, pkg, raw, &parsed); err != nil {
		return nil, raw, err
	}
	return &parsed, raw, nil
}

func decode(op, pkg string, raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return api.DecodeFailure(api.Error{Operation: op, Package: pkg, Message: "decode response: " + err.Error(), Cause: err})
	}
	return nil
}
