package testkit

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"sync"
)

// The Store-image checks read only the header (image.DecodeConfig), so a
// fixture needs exact dimensions and a real format, not real pixels. Encoding
// a full-size RGBA screenshot per call made the image suites some of the
// slowest under -race; a grayscale image at BestSpeed, encoded once per size,
// decodes to the same width, height and format.
var (
	imageMu   sync.Mutex
	imageMemo = map[imageKey][]byte{}
)

type imageKey struct {
	format string
	w, h   int
}

// PNG returns a decodable PNG of exactly w×h. The slice is a fresh copy, so a
// caller may append to it (the byte-cap tests pad a header with filler).
func PNG(w, h int) []byte {
	return encoded(imageKey{"png", w, h}, func(b *bytes.Buffer, img image.Image) error {
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		return enc.Encode(b, img)
	})
}

// JPEG returns a decodable JPEG of exactly w×h, as a fresh copy.
func JPEG(w, h int) []byte {
	return encoded(imageKey{"jpeg", w, h}, func(b *bytes.Buffer, img image.Image) error {
		return jpeg.Encode(b, img, nil)
	})
}

func encoded(k imageKey, encode func(*bytes.Buffer, image.Image) error) []byte {
	imageMu.Lock()
	defer imageMu.Unlock()
	b, ok := imageMemo[k]
	if !ok {
		var buf bytes.Buffer
		if err := encode(&buf, image.NewGray(image.Rect(0, 0, k.w, k.h))); err != nil {
			// Encoding an in-memory image only fails on invalid dimensions,
			// a bug in the calling test rather than a condition to handle.
			panic("testkit: encode " + k.format + ": " + err.Error())
		}
		b = buf.Bytes()
		imageMemo[k] = b
	}
	return append([]byte(nil), b...)
}
