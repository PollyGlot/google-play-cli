// Package imagevalidate_test exercises the offline Store-image rule engine:
// the image analog of the text char-limit validator. Every check is pure and
// offline: dimensions and format come from image.DecodeConfig (header only),
// the rest from a versioned in-code table (ADR-0013 §4).
package imagevalidate_test

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagevalidate"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// pngOf / jpgOf produce a real (decodable) image of exactly w×h so
// image.DecodeConfig reports those dimensions and the format.
func pngOf(w, h int) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

func jpgOf(w, h int) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil)
	return b.Bytes()
}

func slot(data ...[]byte) [][]byte { return data }

// rulesByType indexes problems by rule for assertion.
func hasProblem(ps []imagevalidate.Problem, ty images.Type, rule string) bool {
	for _, p := range ps {
		if p.ImageType == string(ty) && p.Rule == rule {
			return true
		}
	}
	return false
}

// TestValidate_validTree_noProblems is the tracer bullet: an icon at exactly
// 512×512 and two phone screenshots within the side/ratio bounds pass clean.
func TestValidate_validTree_noProblems(t *testing.T) {
	tr := imagetree.Tree{
		"en-US": {
			images.Icon:             slot(pngOf(512, 512)),
			images.FeatureGraphic:   slot(jpgOf(1024, 500)),
			images.TvBanner:         slot(pngOf(1280, 720)),
			images.PhoneScreenshots: slot(pngOf(1080, 1920), jpgOf(1080, 1920)),
		},
	}
	if ps := imagevalidate.Validate(tr); len(ps) != 0 {
		t.Fatalf("valid tree produced problems: %+v", ps)
	}
}

// TestValidate_exactDimensions asserts a singular slot off its required exact
// size is a dimensions violation (icon must be 512×512).
func TestValidate_exactDimensions(t *testing.T) {
	tr := imagetree.Tree{"en-US": {images.Icon: slot(pngOf(500, 500))}}
	ps := imagevalidate.Validate(tr)
	if !hasProblem(ps, images.Icon, "dimensions") {
		t.Errorf("500×500 icon should violate exact dimensions: %+v", ps)
	}
}

// TestValidate_screenshotSideRange asserts a screenshot with a side outside
// [320,3840] is a side violation.
func TestValidate_screenshotSideRange(t *testing.T) {
	tr := imagetree.Tree{"en-US": {images.PhoneScreenshots: slot(pngOf(200, 400))}}
	ps := imagevalidate.Validate(tr)
	if !hasProblem(ps, images.PhoneScreenshots, "side") {
		t.Errorf("200px side should violate the screenshot side range: %+v", ps)
	}
}

// TestValidate_screenshotAspectRatio asserts a screenshot whose long side
// exceeds 2× the short side is a ratio violation.
func TestValidate_screenshotAspectRatio(t *testing.T) {
	tr := imagetree.Tree{"en-US": {images.PhoneScreenshots: slot(pngOf(400, 1600))}} // 4:1
	ps := imagevalidate.Validate(tr)
	if !hasProblem(ps, images.PhoneScreenshots, "ratio") {
		t.Errorf("4:1 screenshot should violate the aspect ratio bound: %+v", ps)
	}
}

// TestValidate_format asserts bytes that are not a decodable PNG/JPEG are a
// format violation.
func TestValidate_format(t *testing.T) {
	tr := imagetree.Tree{"en-US": {images.Icon: slot([]byte("GIF89a not really an image"))}}
	ps := imagevalidate.Validate(tr)
	if !hasProblem(ps, images.Icon, "format") {
		t.Errorf("non-image bytes should violate format: %+v", ps)
	}
}

// TestValidate_perSlotCount asserts a gallery exceeding 8 images is a count
// violation (reported once for the slot).
func TestValidate_perSlotCount(t *testing.T) {
	imgs := make([][]byte, 9)
	for i := range imgs {
		imgs[i] = pngOf(1080, 1920)
	}
	tr := imagetree.Tree{"en-US": {images.PhoneScreenshots: imgs}}
	ps := imagevalidate.Validate(tr)
	if !hasProblem(ps, images.PhoneScreenshots, "count") {
		t.Errorf("9 screenshots should violate the per-slot count (max 8): %+v", ps)
	}
}

// TestValidate_perImageByteCap asserts an image whose byte size exceeds the
// per-type cap is a bytes violation. A valid header followed by filler keeps
// the format/dimensions valid so only the bytes rule fires.
func TestValidate_perImageByteCap(t *testing.T) {
	icon := pngOf(512, 512)
	oversized := append(icon, make([]byte, imagevalidate.MaxBytesFor(images.Icon)+1)...)
	tr := imagetree.Tree{"en-US": {images.Icon: slot(oversized)}}
	ps := imagevalidate.Validate(tr)
	if !hasProblem(ps, images.Icon, "bytes") {
		t.Errorf("oversized icon should violate the byte cap: %+v", ps)
	}
}

// TestRulesVersion_isDatestampedAndCitesDoc asserts the rule table is
// versioned and cites the source: the ADR-0013 §4 "single versioned source"
// requirement.
func TestRulesVersion_isDatestampedAndCitesDoc(t *testing.T) {
	if imagevalidate.RulesVersion == "" {
		t.Error("RulesVersion must be a datestamp")
	}
	if imagevalidate.RulesDoc == "" {
		t.Error("RulesDoc must cite the Google asset-spec URL")
	}
}
