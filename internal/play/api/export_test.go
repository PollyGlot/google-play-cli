package api

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/transport"
)

func ResumableUploadPaced(ctx context.Context, hc *http.Client, op, pkg, initiateURL, contentType string, r io.ReaderAt, size int64, chunkTimeout time.Duration, waits *[]time.Duration) ([]byte, int, error) {
	pace := resumePacing{
		chunkTimeout: chunkTimeout,
		backoff:      transport.Backoff,
		sleep: func(ctx context.Context, d time.Duration) bool {
			if ctx.Err() != nil {
				return false
			}
			*waits = append(*waits, d)
			return true
		},
	}
	return resumableUpload(ctx, hc, op, PackageResource(pkg), initiateURL, contentType, r, size, nil, "", nil, pace)
}
