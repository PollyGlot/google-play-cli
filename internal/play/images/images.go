package images

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opImagesList      = "images.list"
	opImagesUpload    = "images.upload"
	opImagesDelete    = "images.delete"
	opImagesDeleteAll = "images.deleteall"
)

var (
	methodList      = apiregistry.MustResolve("androidpublisher.edits.images.list")
	methodUpload    = apiregistry.MustResolve("androidpublisher.edits.images.upload")
	methodDelete    = apiregistry.MustResolve("androidpublisher.edits.images.delete")
	methodDeleteAll = apiregistry.MustResolve("androidpublisher.edits.images.deleteall")
)

type Type string

const (
	Icon           Type = "icon"
	FeatureGraphic Type = "featureGraphic"
	TvBanner       Type = "tvBanner"

	PhoneScreenshots     Type = "phoneScreenshots"
	SevenInchScreenshots Type = "sevenInchScreenshots"
	TenInchScreenshots   Type = "tenInchScreenshots"
	TvScreenshots        Type = "tvScreenshots"
	WearScreenshots      Type = "wearScreenshots"
)

type AIGeneratedState string

const (
	NotAIGenerated AIGeneratedState = "aiGeneratedStateNotAiGenerated"
	AIGenerated    AIGeneratedState = "aiGeneratedStateAiGeneratedDeveloperAttested"
)

type Spec struct {
	Type    Type
	Gallery bool
}

var specs = []Spec{
	{Type: Icon, Gallery: false},
	{Type: FeatureGraphic, Gallery: false},
	{Type: TvBanner, Gallery: false},
	{Type: PhoneScreenshots, Gallery: true},
	{Type: SevenInchScreenshots, Gallery: true},
	{Type: TenInchScreenshots, Gallery: true},
	{Type: TvScreenshots, Gallery: true},
	{Type: WearScreenshots, Gallery: true},
}

func Types() []Type {
	out := make([]Type, len(specs))
	for i, s := range specs {
		out[i] = s.Type
	}
	return out
}

func SpecOf(t Type) (Spec, bool) {
	for _, s := range specs {
		if s.Type == t {
			return s, true
		}
	}
	return Spec{}, false
}

func (t Type) Gallery() bool {
	s, ok := SpecOf(t)
	return ok && s.Gallery
}

func ParseType(s string) (Type, bool) {
	for _, sp := range specs {
		if string(sp.Type) == s {
			return sp.Type, true
		}
	}
	return "", false
}

var retired = []string{"promoGraphic"}

func Retired(s string) bool {
	for _, r := range retired {
		if r == s {
			return true
		}
	}
	return false
}

func Names() string {
	var b strings.Builder
	for i, s := range specs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(string(s.Type))
	}
	return b.String()
}

type Image struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Sha1   string `json:"sha1"`
	Sha256 string `json:"sha256"`
}

func slotParams(pkg, editID, language string, imageType Type) map[string]string {
	return map[string]string{
		"packageName": pkg,
		"editId":      editID,
		"language":    language,
		"imageType":   string(imageType),
	}
}

func List(ctx context.Context, hc *http.Client, pkg, editID, language string, imageType Type) ([]Image, json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodList, Op: opImagesList, Target: pkg,
		Params: slotParams(pkg, editID, language, imageType),
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed struct {
		Images []Image `json:"images"`
	}
	if err := decode(opImagesList, pkg, raw, &parsed); err != nil {
		return nil, raw, err
	}
	return parsed.Images, raw, nil
}

func Upload(ctx context.Context, hc *http.Client, pkg, editID, language string, imageType Type, data []byte, ai AIGeneratedState) (*Image, error) {
	var q url.Values
	if ai != "" {
		q = url.Values{"aiGeneratedState": {string(ai)}}
	}
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodUpload, Op: opImagesUpload, Target: pkg,
		Params:      slotParams(pkg, editID, language, imageType),
		Query:       q,
		Media:       true,
		Body:        data,
		ContentType: http.DetectContentType(data),
	})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Image Image `json:"image"`
	}
	if err := decode(opImagesUpload, pkg, raw, &parsed); err != nil {
		return nil, err
	}
	return &parsed.Image, nil
}

func Delete(ctx context.Context, hc *http.Client, pkg, editID, language string, imageType Type, imageID string) error {
	params := slotParams(pkg, editID, language, imageType)
	params["imageId"] = imageID
	_, err := api.Do(ctx, hc, api.Call{Method: methodDelete, Op: opImagesDelete, Target: pkg, Params: params})
	return err
}

func DeleteAll(ctx context.Context, hc *http.Client, pkg, editID, language string, imageType Type) error {
	_, err := api.Do(ctx, hc, api.Call{
		Method: methodDeleteAll, Op: opImagesDeleteAll, Target: pkg,
		Params: slotParams(pkg, editID, language, imageType),
	})
	return err
}

func decode(op, pkg string, raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return api.DecodeFailure(api.Error{Operation: op, Package: pkg, Message: "decode response: " + err.Error(), Cause: err})
	}
	return nil
}

func OriginalURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || !strings.HasSuffix(u.Hostname(), ".googleusercontent.com") {
		return raw
	}
	end := len(raw)
	if q := strings.IndexAny(raw, "?#"); q >= 0 {
		end = q
	}
	base, tail := raw[:end], raw[end:]
	seg := strings.LastIndex(base, "/")
	if seg < len(u.Scheme)+3+len(u.Host) || seg == len(base)-1 {
		return raw
	}
	if eq := strings.IndexByte(base[seg:], '='); eq >= 0 {
		base = base[:seg+eq]
	}
	return base + "=s0" + tail
}
