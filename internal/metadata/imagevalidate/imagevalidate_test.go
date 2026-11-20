package imagevalidate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagevalidate"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func slot(data ...[]byte) [][]byte { return data }

func validate(t *testing.T, tr imagetree.Tree) []imagevalidate.Problem {
	t.Helper()
	dir := t.TempDir()
	st := make(imagetree.Staged, len(tr))
	for loc, slots := range tr {
		st[loc] = make(map[images.Type][]string, len(slots))
		for ty, seq := range slots {
			for i, data := range seq {
				p := filepath.Join(dir, fmt.Sprintf("%s-%s-%d", loc, ty, i))
				if err := os.WriteFile(p, data, 0o600); err != nil {
					t.Fatal(err)
				}
				st[loc][ty] = append(st[loc][ty], p)
			}
		}
	}
	ps, err := imagevalidate.Validate(st)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return ps
}

func hasProblem(ps []imagevalidate.Problem, ty images.Type, rule string) bool {
	for _, p := range ps {
		if p.ImageType == string(ty) && p.Rule == rule {
			return true
		}
	}
	return false
}

func TestValidate_validTree_noProblems(t *testing.T) {
	tr := imagetree.Tree{
		"en-US": {
			images.Icon:             slot(testkit.PNG(512, 512)),
			images.FeatureGraphic:   slot(testkit.JPEG(1024, 500)),
			images.TvBanner:         slot(testkit.PNG(1280, 720)),
			images.PhoneScreenshots: slot(testkit.PNG(1080, 1920), testkit.JPEG(1080, 1920)),
		},
	}
	if ps := validate(t, tr); len(ps) != 0 {
		t.Fatalf("valid tree produced problems: %+v", ps)
	}
}

func TestValidate_exactDimensions(t *testing.T) {
	tr := imagetree.Tree{"en-US": {images.Icon: slot(testkit.PNG(500, 500))}}
	ps := validate(t, tr)
	if !hasProblem(ps, images.Icon, "dimensions") {
		t.Errorf("500×500 icon should violate exact dimensions: %+v", ps)
	}
}

func TestValidate_screenshotSideRange(t *testing.T) {
	tr := imagetree.Tree{"en-US": {images.PhoneScreenshots: slot(testkit.PNG(200, 400))}}
	ps := validate(t, tr)
	if !hasProblem(ps, images.PhoneScreenshots, "side") {
		t.Errorf("200px side should violate the screenshot side range: %+v", ps)
	}
}

func TestValidate_screenshotAspectRatio(t *testing.T) {
	tr := imagetree.Tree{"en-US": {images.PhoneScreenshots: slot(testkit.PNG(400, 1600))}}
	ps := validate(t, tr)
	if !hasProblem(ps, images.PhoneScreenshots, "ratio") {
		t.Errorf("4:1 screenshot should violate the aspect ratio bound: %+v", ps)
	}
}

func TestValidate_format(t *testing.T) {
	tr := imagetree.Tree{"en-US": {images.Icon: slot([]byte("GIF89a not really an image"))}}
	ps := validate(t, tr)
	if !hasProblem(ps, images.Icon, "format") {
		t.Errorf("non-image bytes should violate format: %+v", ps)
	}
}

func TestValidate_perSlotCount(t *testing.T) {
	imgs := make([][]byte, 9)
	for i := range imgs {
		imgs[i] = testkit.PNG(1080, 1920)
	}
	tr := imagetree.Tree{"en-US": {images.PhoneScreenshots: imgs}}
	ps := validate(t, tr)
	if !hasProblem(ps, images.PhoneScreenshots, "count") {
		t.Errorf("9 screenshots should violate the per-slot count (max 8): %+v", ps)
	}
}

func TestValidate_perImageByteCap(t *testing.T) {
	icon := testkit.PNG(512, 512)
	oversized := append(icon, make([]byte, imagevalidate.MaxBytesFor(images.Icon)+1)...)
	tr := imagetree.Tree{"en-US": {images.Icon: slot(oversized)}}
	ps := validate(t, tr)
	if !hasProblem(ps, images.Icon, "bytes") {
		t.Errorf("oversized icon should violate the byte cap: %+v", ps)
	}
}

func TestRulesVersion_isDatestampedAndCitesDoc(t *testing.T) {
	if imagevalidate.RulesVersion == "" {
		t.Error("RulesVersion must be a datestamp")
	}
	if imagevalidate.RulesDoc == "" {
		t.Error("RulesDoc must cite the Google asset-spec URL")
	}
}

func TestCheckImage_matchesValidate(t *testing.T) {
	oversized := append(testkit.PNG(512, 512), make([]byte, imagevalidate.MaxBytesFor(images.Icon))...)
	cases := []struct {
		ty   images.Type
		data []byte
	}{
		{images.Icon, testkit.PNG(512, 512)},
		{images.Icon, testkit.PNG(1024, 512)},
		{images.Icon, oversized},
		{images.Icon, []byte("not an image")},
		{images.PhoneScreenshots, testkit.PNG(400, 1600)},
		{images.PhoneScreenshots, testkit.JPEG(1080, 1920)},
	}
	for _, c := range cases {
		want := validate(t, imagetree.Tree{"en-US": {c.ty: slot(c.data)}})
		got := imagevalidate.CheckImage("en-US", c.ty, 0, c.data)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s (%d bytes): CheckImage = %v, Validate = %v", c.ty, len(c.data), got, want)
		}
	}
}
