package addcmd

import (
	"fmt"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

var retryableExitCodes = map[int]struct{}{
	40: {},
	50: {},
}

func isRetryable(code int) bool {
	_, ok := retryableExitCodes[code]
	return ok
}

type aggregateError struct {
	results []pkgResult
}

func newAggregateError(results []pkgResult) *aggregateError {
	for _, r := range results {
		if r.err != nil {
			return &aggregateError{results: results}
		}
	}
	return nil
}

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
	return 1
}

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
