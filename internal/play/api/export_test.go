package api

import (
	"context"
	"testing"
	"time"
)

// SetResumeTiming swaps the resumable helper's pacing for the duration of t:
// chunkTimeout bounds one chunk PUT, and every backoff wait (still computed by
// the production curve) is recorded into *waits instead of slept. The wait
// still reports a done ctx, as production's does. Tests using it must not run
// in parallel with each other.
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
