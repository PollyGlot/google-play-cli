package testkit

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"sync"
)

var (
	imageMu   sync.Mutex
	imageMemo = map[imageKey][]byte{}
)

type imageKey struct {
	format string
	w, h   int
}

func PNG(w, h int) []byte {
	return encoded(imageKey{"png", w, h}, func(b *bytes.Buffer, img image.Image) error {
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		return enc.Encode(b, img)
	})
}

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
			panic("testkit: encode " + k.format + ": " + err.Error())
		}
		b = buf.Bytes()
		imageMemo[k] = b
	}
	return append([]byte(nil), b...)
}
