package fanout

import (
	"sync"
	"sync/atomic"
)

const Limit = 4

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
