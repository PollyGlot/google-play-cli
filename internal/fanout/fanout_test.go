package fanout_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/fanout"
)

// TestEach_callsEveryIndexOnce: each index runs exactly once, whatever n is
// relative to Limit, and results written by index keep the input order.
func TestEach_callsEveryIndexOnce(t *testing.T) {
	for _, n := range []int{0, 1, fanout.Limit - 1, fanout.Limit, fanout.Limit + 1, 100} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			hits := make([]atomic.Int32, n)
			out := make([]int, n)
			if err := fanout.Each(n, func(i int) error {
				hits[i].Add(1)
				out[i] = i * i
				return nil
			}); err != nil {
				t.Fatalf("Each: %v", err)
			}
			for i := range n {
				if hits[i].Load() != 1 {
					t.Errorf("index %d ran %d times, want 1", i, hits[i].Load())
				}
				if out[i] != i*i {
					t.Errorf("out[%d] = %d, want %d", i, out[i], i*i)
				}
			}
		})
	}
}

// TestEach_keepsExactlyLimitInFlight proves the bound both ways without a
// timing guess: the first calls block until Limit of them are inside at once
// (a pool narrower than Limit never opens the gate and fails on the timeout),
// and the peak never exceeds Limit.
func TestEach_keepsExactlyLimitInFlight(t *testing.T) {
	var (
		inflight, peak atomic.Int32
		once           sync.Once
		gate           = make(chan struct{})
	)
	err := fanout.Each(fanout.Limit*3, func(int) error {
		cur := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		if cur == fanout.Limit {
			once.Do(func() { close(gate) })
		}
		select {
		case <-gate:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("never reached Limit concurrent calls")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got != fanout.Limit {
		t.Errorf("peak in-flight = %d, want exactly %d", got, fanout.Limit)
	}
}

// TestEach_returnsLowestIndexError: index 7 fails first in time, index 5 fails
// later; the serial loop would have stopped at 5, so 5's error must win, and no
// index is started once the failure is known beyond those already in flight.
func TestEach_returnsLowestIndexError(t *testing.T) {
	err5, err7 := errors.New("five"), errors.New("seven")
	var started atomic.Int32
	err := fanout.Each(50, func(i int) error {
		started.Add(1)
		switch i {
		case 5:
			time.Sleep(50 * time.Millisecond)
			return err5
		case 7:
			return err7
		}
		return nil
	})
	if !errors.Is(err, err5) {
		t.Fatalf("Each err = %v, want the lowest-index failure %v", err, err5)
	}
	if n := started.Load(); n >= 50 {
		t.Errorf("started %d calls after a failure, want an early stop", n)
	}
}

// TestEach_repanicsOnCallerGoroutine: a panic in fn must surface where the
// caller's defers can see it (edits.WithReadOnlyEdit discards its Edit in a
// defer), not crash the process from a worker goroutine.
func TestEach_repanicsOnCallerGoroutine(t *testing.T) {
	defer func() {
		if r := recover(); r != "boom" {
			t.Fatalf("recovered %v, want the worker's panic value", r)
		}
	}()
	_ = fanout.Each(10, func(i int) error {
		if i == 3 {
			panic("boom")
		}
		return nil
	})
	t.Fatal("Each returned normally after a panic in fn")
}
