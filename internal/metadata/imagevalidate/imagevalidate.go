package imagevalidate

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

const RulesVersion = "2026-06-01"

const RulesDoc = "https://support.google.com/googleplay/android-developer/answer/9866151"

const (
	screenshotMinSide  = 320
	screenshotMaxSide  = 3840
	screenshotMaxRatio = 2.0
)

type rule struct {
	exactW, exactH int
	screenshot     bool
	maxCount       int
	maxBytes       int64
}

const (
	mib = 1 << 20
)

var rules = map[images.Type]rule{
	images.Icon:                 {exactW: 512, exactH: 512, maxCount: 1, maxBytes: 1 * mib},
	images.FeatureGraphic:       {exactW: 1024, exactH: 500, maxCount: 1, maxBytes: 15 * mib},
	images.TvBanner:             {exactW: 1280, exactH: 720, maxCount: 1, maxBytes: 15 * mib},
	images.PhoneScreenshots:     {screenshot: true, maxCount: 8, maxBytes: 8 * mib},
	images.SevenInchScreenshots: {screenshot: true, maxCount: 8, maxBytes: 8 * mib},
	images.TenInchScreenshots:   {screenshot: true, maxCount: 8, maxBytes: 8 * mib},
	images.TvScreenshots:        {screenshot: true, maxCount: 8, maxBytes: 8 * mib},
	images.WearScreenshots:      {screenshot: true, maxCount: 8, maxBytes: 8 * mib},
}

func MaxBytesFor(ty images.Type) int64 { return rules[ty].maxBytes }

type Problem struct {
	Locale    string `json:"locale"`
	ImageType string `json:"imageType"`
	Index     int    `json:"index"`
	Rule      string `json:"rule"`
	Message   string `json:"message"`
}

func Validate(st imagetree.Staged) ([]Problem, error) {
	var problems []Problem
	locales := make([]string, 0, len(st))
	for loc := range st {
		locales = append(locales, loc)
	}
	sort.Strings(locales)

	for _, loc := range locales {
		slots := st[loc]
		for _, ty := range images.Types() {
			seq := slots[ty]
			if len(seq) == 0 {
				continue
			}
			r := rules[ty]
			if r.maxCount > 0 && len(seq) > r.maxCount {
				problems = append(problems, Problem{
					Locale: loc, ImageType: string(ty), Index: -1, Rule: "count",
					Message: fmt.Sprintf("slot has %d images; max %d", len(seq), r.maxCount),
				})
			}
			for i, path := range seq {
				ps, err := checkImage(loc, ty, i, path, r)
				if err != nil {
					return nil, err
				}
				problems = append(problems, ps...)
			}
		}
	}
	return problems, nil
}

func CheckImage(loc string, ty images.Type, i int, data []byte) []Problem {
	return checkContent(loc, ty, i, int64(len(data)), bytes.NewReader(data), rules[ty])
}

func checkImage(loc string, ty images.Type, i int, path string, r rule) ([]Problem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return checkContent(loc, ty, i, info.Size(), bufio.NewReader(f), r), nil
}

func checkContent(loc string, ty images.Type, i int, size int64, src io.Reader, r rule) []Problem {
	var out []Problem
	mk := func(ruleName, msg string) {
		out = append(out, Problem{Locale: loc, ImageType: string(ty), Index: i, Rule: ruleName, Message: msg})
	}

	if r.maxBytes > 0 && size > r.maxBytes {
		mk("bytes", fmt.Sprintf("%d bytes exceeds the %d-byte cap", size, r.maxBytes))
	}

	cfg, format, err := image.DecodeConfig(src)
	if err != nil {
		mk("format", "not a decodable PNG or JPEG image")
		return out
	}
	if format != "png" && format != "jpeg" {
		mk("format", fmt.Sprintf("format %q is not PNG or JPEG", format))
		return out
	}

	if r.exactW > 0 && (cfg.Width != r.exactW || cfg.Height != r.exactH) {
		mk("dimensions", fmt.Sprintf("expected %d×%d, got %d×%d", r.exactW, r.exactH, cfg.Width, cfg.Height))
	}

	if r.screenshot {
		if cfg.Width < screenshotMinSide || cfg.Width > screenshotMaxSide ||
			cfg.Height < screenshotMinSide || cfg.Height > screenshotMaxSide {
			mk("side", fmt.Sprintf("each side must be %d–%d px, got %d×%d", screenshotMinSide, screenshotMaxSide, cfg.Width, cfg.Height))
		}
		long, short := cfg.Width, cfg.Height
		if short > long {
			long, short = short, long
		}
		if short > 0 && float64(long)/float64(short) > screenshotMaxRatio {
			mk("ratio", fmt.Sprintf("aspect ratio %d×%d exceeds the %.0f:1 bound", cfg.Width, cfg.Height, screenshotMaxRatio))
		}
	}
	return out
}
