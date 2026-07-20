package addcmd

import (
	"fmt"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// retryableExitCodes are the docs/DESIGN.md §9 exit codes an automated
// caller can usefully retry unchanged: 40 (API 5xx, upstream temporarily
// unhealthy) and 50 (network — timeout, DNS, refused). Everything else
// (auth, authorization, client validation, other 4xx) is permanent for
// the current inputs and credentials. 60 (state conflict) is only
// "sometimes" retryable per §9, so it is deliberately treated as
// non-retryable here — surfacing it stops a blind retry loop, which is
// the safer default for a batch write.
var retryableExitCodes = map[int]struct{}{
	40: {},
	50: {},
}

func isRetryable(code int) bool {
	_, ok := retryableExitCodes[code]
	return ok
}

// aggregateError is the typed error a multi-package `apps add` returns
// when at least one package failed. It carries every per-package outcome
// and computes the batch exit code by the non-retryable-wins rule.
//
// Why non-retryable-wins: `apps add a b c` is a batch of independent
// writes. If `b` failed with a permanent error (e.g. 403 — the service
// account is not invited on `b`) while `a` and `c` registered, retrying
// the whole batch would just re-hit `b`'s permanent failure. Reporting a
// non-retryable exit code tells the agent driving the CLI to inspect and
// fix, not to loop. Only when *every* failure is retryable (all 5xx or
// all network) does the batch report a retryable code, inviting a retry
// once the transient condition clears.
type aggregateError struct {
	results []pkgResult
}

// newAggregateError builds an aggregateError from batch results, or
// returns nil when every package succeeded (the caller then returns a
// clean nil error / exit 0).
func newAggregateError(results []pkgResult) *aggregateError {
	for _, r := range results {
		if r.err != nil {
			return &aggregateError{results: results}
		}
	}
	return nil
}

// ExitCode implements exit.Coder with the non-retryable-wins rule: the
// first non-retryable failure's code wins; if all failures are retryable,
// the first retryable code is used. "First" is in deduplicated argument
// order, so the exit code is deterministic for a given command line.
func (e *aggregateError) ExitCode() int {
	firstNonRetryable := 0
	firstRetryable := 0
	for _, r := range e.results {
		if r.err == nil {
			continue
		}
		code := exit.For(r.err)
		if isRetryable(code) {
			if firstRetryable == 0 {
				firstRetryable = code
			}
			continue
		}
		if firstNonRetryable == 0 {
			firstNonRetryable = code
		}
	}
	if firstNonRetryable != 0 {
		return firstNonRetryable
	}
	if firstRetryable != 0 {
		return firstRetryable
	}
	// Defensive: newAggregateError only builds this when a failure exists,
	// so a failure whose exit.For is 0 (no Coder in its chain) still yields
	// a non-zero code rather than a misleading exit 0.
	return 1
}

// Error is a one-line summary naming the failed packages. The per-package
// detail (each error and its exit code) is printed by reportBatch to
// stderr during Run; this keeps the returned error concise so the kernel's
// final "Error: ..." line does not duplicate the full ✗ list.
func (e *aggregateError) Error() string {
	var failed []string
	for _, r := range e.results {
		if r.err != nil {
			failed = append(failed, r.pkg)
		}
	}
	return fmt.Sprintf("apps add: %d of %d packages failed: %s",
		len(failed), len(e.results), strings.Join(failed, ", "))
}
