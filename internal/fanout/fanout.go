// Package fanout runs independent READ calls with a small fixed concurrency,
// so a sweep that costs one round trip per slot (images list/pull) or per app
// (apps audit) pays about a quarter of the serial wall time.
//
// The contract is shaped by what the callers must keep (#602):
//
//   - Limit is fixed at 4 and has no flag: a knob is a public contract, and
//     four in-flight reads is a burst the Play quota absorbs. --retry stays
//     opt-in, so a 429 in the burst fails the run exactly as a serial 429
//     would; fanout neither retries nor changes that.
//   - Callers write result i into their own slot i, so output order is the
//     input order, never the completion order.
//   - The error returned is the one the serial loop would have returned: the
//     failure with the LOWEST index. Indices are handed out in order and a
//     started call always runs to completion, so every index below the first
//     failure in time has finished by the time Each returns; the lowest
//     failing index is therefore the same on every run with the same
//     responses. After a failure no new index starts (at most Limit-1 calls
//     already in flight finish), like the serial loop's early return.
//   - A panic in fn is re-raised on the caller's goroutine once every worker
//     has stopped, so a deferred cleanup up the stack (the discard of a
//     read-only Edit, edits.WithReadOnlyEdit) still runs instead of the
//     process dying on a worker goroutine with the Edit left open for 24h.
//
// Only reads belong here: writes inside one Edit stay sequential (#602
// decision), since Google does not document concurrent writes to an Edit.
package fanout

import (
	"sync"
	"sync/atomic"
)

// Limit is the fixed number of calls Each keeps in flight.
const Limit = 4

// Each calls fn(i) for every i in [0, n) with at most Limit calls running at
// once, and returns the error of the lowest failing index (nil when every call
// succeeded). fn must only touch state owned by index i, or guard shared state
// itself.
func Each(n int, fn func(i int) error) error {
	if n <= 0 {
		return nil
	}
	errs := make([]error, n)
	var (
		next    atomic.Int64
		stopped atomic.Bool
		wg      sync.WaitGroup

		panicOnce sync.Once
		panicked  any
	)
	for range min(Limit, n) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicOnce.Do(func() { panicked = r })
					stopped.Store(true)
				}
			}()
			for !stopped.Load() {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				if err := fn(i); err != nil {
					errs[i] = err
					stopped.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if panicked != nil {
		panic(panicked)
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
