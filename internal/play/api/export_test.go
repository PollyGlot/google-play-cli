package api

import (
	"context"
	"testing"
	"time"
)

func SetResumeTiming(t *testing.T, chunkTimeout time.Duration, waits *[]time.Duration) {
	t.Helper()
	prevChunk, prevSleep := resumeChunkTimeout, resumeSleep
	t.Cleanup(func() { resumeChunkTimeout, resumeSleep = prevChunk, prevSleep })
	resumeChunkTimeout = chunkTimeout
	resumeSleep = func(ctx context.Context, d time.Duration) bool {
		if ctx.Err() != nil {
			return false
		}
		*waits = append(*waits, d)
		return true
	}
}
