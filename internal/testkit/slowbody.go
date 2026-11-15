package testkit

import (
	"context"
	"io"
	"time"
)

// TrickleBody serves body in chunk-byte reads with gap between them, so a
// download takes about len(body)/chunk*gap in total while never going silent
// for longer than gap. It proves a transfer is bounded by inactivity, not by
// total duration. Pass the request's context: once it ends, a read fails as it
// would on a real transport.
func TrickleBody(ctx context.Context, body []byte, chunk int, gap time.Duration) io.ReadCloser {
	return io.NopCloser(&trickle{ctx: ctx, rest: body, chunk: chunk, gap: gap})
}

type trickle struct {
	ctx   context.Context
	rest  []byte
	chunk int
	gap   time.Duration
}

func (t *trickle) Read(p []byte) (int, error) {
	if len(t.rest) == 0 {
		return 0, io.EOF
	}
	select {
	case <-t.ctx.Done():
		return 0, t.ctx.Err()
	case <-time.After(t.gap):
	}
	n := min(t.chunk, len(p), len(t.rest))
	copy(p, t.rest[:n])
	t.rest = t.rest[n:]
	return n, nil
}

// StalledBody serves head, then blocks until ctx ends, as a connection that
// stops moving bytes without a reset does. Pass the request's context, so the
// read fails the way it would on a real transport once a deadline cancels it.
func StalledBody(ctx context.Context, head []byte) io.ReadCloser {
	return io.NopCloser(&stalled{ctx: ctx, head: head})
}

type stalled struct {
	ctx  context.Context
	head []byte
}

func (s *stalled) Read(p []byte) (int, error) {
	if len(s.head) > 0 {
		n := copy(p, s.head)
		s.head = s.head[n:]
		return n, nil
	}
	<-s.ctx.Done()
	return 0, s.ctx.Err()
}
