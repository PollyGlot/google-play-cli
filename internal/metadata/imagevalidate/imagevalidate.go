// Package imagevalidate is the offline Store-image rule engine: the image
// analog of internal/metadata/validate's char-limit check. The edits.images
// API exposes no endpoint that returns its asset rules: dimensions, ratios,
// byte sizes, and counts live only in Google's published docs and are
// enforced only at upload/commit, so this package encodes them as a
// VERSIONED, datestamped table in code (single source, citing the doc URL)
// and checks them all OFFLINE: no credentials, no network. Dimensions and
// format are read via the stdlib image.DecodeConfig (header only, no pixel
// decode, no external dependency: ADR-0007), with the png/jpeg decoders
// registered by blank import.
//
// The checks are deliberately strict ("respect the guidelines to the pixel"),
// but Play stays the ultimate authority: `images apply` runs this as a
// fail-fast pre-check that `--skip-preflight` bypasses, so a stale table can
// never permanently block an upload Play would accept (ADR-0013 §4).
package imagevalidate

import (
	"bufio"
	"fmt"
	"image"
	_ "image/jpeg" // register the JPEG decoder for image.DecodeConfig
	_ "image/png"  // register the PNG decoder for image.DecodeConfig
	"os"
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// RulesVersion is the datestamp this rule table was last reconciled with
// Google's published Store-asset specifications. Bump it whenever a limit
// below is changed to match a doc update.
const RulesVersion = "2026-06-01"

// RulesDoc is the Google Play help page the limits below are transcribed
// from (the "preview assets / graphic assets" specification).
const RulesDoc = "https://support.google.com/googleplay/android-developer/answer/9866151"

// Screenshot bounds shared by every gallery type. Google requires each
// screenshot side within [320, 3840] px, with the longest side no more than
// twice the shortest (a 2:1 aspect-ratio bound).
const (
	screenshotMinSide  = 320
	screenshotMaxSide  = 3840
	screenshotMaxRatio = 2.0
)

// rule is the per-type rule row. exactW/exactH of 0 means "no exact-dimension
// rule" (ADR-0013 §4 fixes exact sizes only for icon, featureGraphic, and
// tvBanner). screenshot toggles the side-range + ratio checks. maxCount is the
// per-slot image cap (1 for singular slots, 8 for galleries). maxBytes is the
// per-image byte cap.
type rule struct {
	exactW, exactH int
	screenshot     bool
	maxCount       int
	maxBytes       int64
}

const (
	mib = 1 << 20
)

// rules is the canonical, versioned rule table (see RulesVersion / RulesDoc).
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

// MaxBytesFor returns the per-image byte cap for a type (0 if the type is
// unknown). Exposed so callers and tests can reference the table's single
// source rather than hard-coding a number.
func MaxBytesFor(ty images.Type) int64 { return rules[ty].maxBytes }

// Problem is one rule violation on one image (or, for the count rule, one
// slot). Index is the 0-based position within the slot, or -1 for a
// slot-level problem. Rule names the violated rule
// ("dimensions"/"side"/"ratio"/"format"/"bytes"/"count"); Message states the
// expected-vs-actual.
type Problem struct {
	Locale    string `json:"locale"`
	ImageType string `json:"imageType"`
	Index     int    `json:"index"`
	Rule      string `json:"rule"`
	Message   string `json:"message"`
}

// Validate checks every image in tr against the versioned rule table, fully
// offline. It returns one Problem per violation, in (locale, canonical type,
// position) order, so a CI log shows the full picture in one run. An empty
// tree (no images) yields no problems: required-slot minimums are Play's job
// at commit, not this offline lint (ADR-0013 §4).
//
// It opens one file at a time and reads only its size and its header, so a
// tree of any size costs a few KiB of memory. The error is an unreadable
// file, never a rule violation.
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

// checkImage runs every per-image rule for the image at path, returning its
// violations. A format failure short-circuits the dimension checks (we cannot
// trust width/height from bytes we could not decode).
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

	var out []Problem
	mk := func(ruleName, msg string) {
		out = append(out, Problem{Locale: loc, ImageType: string(ty), Index: i, Rule: ruleName, Message: msg})
	}

	if size := info.Size(); r.maxBytes > 0 && size > r.maxBytes {
		mk("bytes", fmt.Sprintf("%d bytes exceeds the %d-byte cap", size, r.maxBytes))
	}

	cfg, format, err := image.DecodeConfig(bufio.NewReader(f))
	if err != nil {
		mk("format", "not a decodable PNG or JPEG image")
		return out, nil
	}
	if format != "png" && format != "jpeg" {
		// Unreachable while only png/jpeg decoders are registered, but kept
		// explicit so the format contract is enforced, not implied.
		mk("format", fmt.Sprintf("format %q is not PNG or JPEG", format))
		return out, nil
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
	return out, nil
}
