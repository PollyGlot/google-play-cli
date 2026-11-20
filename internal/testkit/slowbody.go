package testkit

import (
	"context"
	"io"
	"time"
)

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
