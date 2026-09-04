// Package images reads and writes the per-locale Store images of a Google
// Play app within an open Edit (edits.images). It is the image sibling of
// internal/play/listings: every function runs inside an Edit the caller has
// already opened (edits.WithEdit / edits.WithReadOnlyEdit) and none opens or
// commits an Edit of its own.
//
// The four verbs map 1:1 onto the API and back the metadata images commands:
//
//   - List   (per (locale, imageType) slot)  → images list / pull / apply diff
//   - Upload (one image into a slot)          → images apply (additive)
//   - Delete (one image by server id)         → images apply --prune
//   - DeleteAll (every image in a slot)       → images apply (gallery reorder)
//
// gplay speaks edits.images over raw HTTP (ADR-0007), reusing the api.Error
// envelope and the body-size caps every other internal/play/* module uses.
package images

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opImagesList      = "images.list"
	opImagesUpload    = "images.upload"
	opImagesDelete    = "images.delete"
	opImagesDeleteAll = "images.deleteall"
)

// Registry entries this package calls. Resolving them at init makes an
// unregistered or vanished method a startup panic caught by CI rather than a
// runtime surprise; verb and URL then come from the Discovery snapshot instead
// of literals kept here (#513). list and deleteall share one template (the
// slot itself); delete adds {imageId}; upload lives on the /upload host.
var (
	methodList      = apiregistry.MustResolve("androidpublisher.edits.images.list")
	methodUpload    = apiregistry.MustResolve("androidpublisher.edits.images.upload")
	methodDelete    = apiregistry.MustResolve("androidpublisher.edits.images.delete")
	methodDeleteAll = apiregistry.MustResolve("androidpublisher.edits.images.deleteall")
)

// Type is one of the nine AppImageType values edits.images exposes. The
// string value IS the API path segment (and the on-disk slot name). There is
// no Chromebook type: androidpublisher v3's AppImageType enum does not
// expose it (PRD #112 "Hors scope"), so it is deliberately not addressable.
type Type string

const (
	// Singular slots hold at most one image.
	Icon           Type = "icon"
	FeatureGraphic Type = "featureGraphic"
	TvBanner       Type = "tvBanner"
	PromoGraphic   Type = "promoGraphic"

	// Gallery slots hold an ordered sequence (Play caps each at 8). Display
	// order is upload order: the API carries no position field (ADR-0013).
	PhoneScreenshots     Type = "phoneScreenshots"
	SevenInchScreenshots Type = "sevenInchScreenshots"
	TenInchScreenshots   Type = "tenInchScreenshots"
	TvScreenshots        Type = "tvScreenshots"
	WearScreenshots      Type = "wearScreenshots"
)

// Spec is the static metadata of one image Type: its singular/gallery nature.
// A singular slot holds ≤1 image; a gallery is an ordered set of ≤8: the
// distinction that decides whether `pull` writes `<type>.<ext>` or a
// `<type>/N.<ext>` directory, and whether `apply` reconciles a value or an
// ordered sequence.
type Spec struct {
	Type    Type
	Gallery bool
}

// specs is the canonical, ordered type table: the four singular slots first,
// then the five galleries. This order is the display order used by
// `images list` and `images pull`.
var specs = []Spec{
	{Type: Icon, Gallery: false},
	{Type: FeatureGraphic, Gallery: false},
	{Type: TvBanner, Gallery: false},
	{Type: PromoGraphic, Gallery: false},
	{Type: PhoneScreenshots, Gallery: true},
	{Type: SevenInchScreenshots, Gallery: true},
	{Type: TenInchScreenshots, Gallery: true},
	{Type: TvScreenshots, Gallery: true},
	{Type: WearScreenshots, Gallery: true},
}

// Specs returns the canonical type table in display order (a copy, so callers
// cannot mutate the package table).
func Specs() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

// Types returns the nine image Types in canonical display order.
func Types() []Type {
	out := make([]Type, len(specs))
	for i, s := range specs {
		out[i] = s.Type
	}
	return out
}

// SpecOf returns the Spec for t and whether t is one of the nine known types.
func SpecOf(t Type) (Spec, bool) {
	for _, s := range specs {
		if s.Type == t {
			return s, true
		}
	}
	return Spec{}, false
}

// Gallery reports whether t is a gallery slot (ordered, ≤8 images). An
// unknown type reports false.
func (t Type) Gallery() bool {
	s, ok := SpecOf(t)
	return ok && s.Gallery
}

// ParseType maps a user-supplied --type value to a known Type. The second
// return is false for anything outside the nine (e.g. "chromebookScreenshots"),
// so the caller can refuse a typo rather than build an invalid API path.
func ParseType(s string) (Type, bool) {
	for _, sp := range specs {
		if string(sp.Type) == s {
			return sp.Type, true
		}
	}
	return "", false
}

// Image is the API-shaped edits.images resource: one per stored image. The
// json tags mirror the Google Play Developer API verbatim per ADR-0003 (JSON
// output is API pass-through). sha256 is the content hash gplay reconciles by
// (ADR-0013); id is the server-assigned handle Delete targets; url is where
// pull downloads the bytes from (images.list returns no original filename).
type Image struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Sha1   string `json:"sha1"`
	Sha256 string `json:"sha256"`
}

// slotParams is the path-parameter set every edits.images method shares: the
// slot is (package, edit, locale, image type). Delete adds imageId on top.
func slotParams(pkg, editID, language string, imageType Type) map[string]string {
	return map[string]string{
		"packageName": pkg,
		"editId":      editID,
		"language":    language,
		"imageType":   string(imageType),
	}
}

// List fetches one slot's images at edits.images.list (GET on the slot URL).
// The API envelope is {"images":[ {...}, ... ]}; List parses `.images` into an
// Image slice in display (list) order and returns the raw body verbatim for
// the --output json pass-through (ADR-0003). An absent slot returns an empty
// slice, no error (missing == empty, ADR-0013).
func List(ctx context.Context, hc *http.Client, pkg, editID, language string, imageType Type) ([]Image, json.RawMessage, error) {
	u, err := methodList.URL(slotParams(pkg, editID, language, imageType))
	if err != nil {
		return nil, nil, &api.Error{Operation: opImagesList, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, methodList.Verb, u, nil)
	if err != nil {
		return nil, nil, &api.Error{Operation: opImagesList, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &api.Error{Operation: opImagesList, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, nil, &api.Error{Operation: opImagesList, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, nil, &api.Error{Operation: opImagesList, Package: pkg, StatusCode: resp.StatusCode, Message: "read response: " + readErr.Error(), Cause: readErr}
	}
	var parsed struct {
		Images []Image `json:"images"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, &api.Error{Operation: opImagesList, Package: pkg, StatusCode: resp.StatusCode, Message: "decode response: " + err.Error(), Cause: err}
	}
	return parsed.Images, raw, nil
}

// Upload sends one image's bytes into a slot at edits.images.upload, using the
// same simple-media protocol as bundles.upload (the upload sub-host,
// uploadType=media, Content-Type: application/octet-stream). It returns the
// Image Google created: crucially its sha256, which `apply` compares against
// the local file's hash. images.upload APPENDS to a gallery (there is no
// position parameter); ordering is the caller's job (upload in name order
// after a DeleteAll: ADR-0013).
func Upload(ctx context.Context, hc *http.Client, pkg, editID, language string, imageType Type, data []byte) (*Image, error) {
	u, err := methodUpload.UploadURL(slotParams(pkg, editID, language, imageType))
	if err != nil {
		return nil, &api.Error{Operation: opImagesUpload, Package: pkg, Message: err.Error(), Cause: err}
	}
	u += "?uploadType=media"
	req, err := http.NewRequestWithContext(ctx, methodUpload.Verb, u, bytes.NewReader(data))
	if err != nil {
		return nil, &api.Error{Operation: opImagesUpload, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.ContentLength = int64(len(data))
	// GetBody lets the transport replay the body across redirects and an
	// oauth2 token-refresh retry (net/http closes Request.Body each attempt).
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: opImagesUpload, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, &api.Error{Operation: opImagesUpload, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	var parsed struct {
		Image Image `json:"image"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, &api.Error{Operation: opImagesUpload, Package: pkg, StatusCode: resp.StatusCode, Message: "decode response: " + err.Error(), Cause: err}
	}
	return &parsed.Image, nil
}

// Delete removes a single image from a slot at edits.images.delete (DELETE on
// the slot URL + "/{imageId}"). Used by `apply --prune` to drop a live image
// whose content is in no local file. A 2xx/204 with no body is expected.
func Delete(ctx context.Context, hc *http.Client, pkg, editID, language string, imageType Type, imageID string) error {
	params := slotParams(pkg, editID, language, imageType)
	params["imageId"] = imageID
	u, err := methodDelete.URL(params)
	if err != nil {
		return &api.Error{Operation: opImagesDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, methodDelete.Verb, u, nil)
	if err != nil {
		return &api.Error{Operation: opImagesDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &api.Error{Operation: opImagesDelete, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return &api.Error{Operation: opImagesDelete, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	return nil
}

// DeleteAll removes every image in a slot at edits.images.deleteall (DELETE on
// the slot URL, no image id). It is the only way to reorder a gallery:
// `apply` does DeleteAll + re-upload in name order (ADR-0013), and the
// destructive primitive behind a full-slot clear. The {"deleted":[...]} body
// is not parsed; a non-2xx surfaces as an *api.Error.
func DeleteAll(ctx context.Context, hc *http.Client, pkg, editID, language string, imageType Type) error {
	u, err := methodDeleteAll.URL(slotParams(pkg, editID, language, imageType))
	if err != nil {
		return &api.Error{Operation: opImagesDeleteAll, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, methodDeleteAll.Verb, u, nil)
	if err != nil {
		return &api.Error{Operation: opImagesDeleteAll, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &api.Error{Operation: opImagesDeleteAll, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return &api.Error{Operation: opImagesDeleteAll, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	return nil
}
