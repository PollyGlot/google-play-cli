// Package doctor runs ordered diagnostic checks against a resolved
// service-account credential and reports a structured result per check.
//
// The doctor sequence is documented in docs/DESIGN.md §1. Checks run in
// order and the chain stops on the first hard failure: subsequent checks
// are reported as Skipped: true with Passed: false so the command layer
// (and JSON consumers) can render every step deterministically.
//
// Each check is a Check value (name + exit-code + run func). The
// non-API portion of `gplay auth doctor` (issue #11) is DefaultChecks;
// the per-package round-trip (issue #12) is one more Check appended to
// the slice — the runner itself does not need to change.
package doctor

import (
	"context"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
)

// CheckResult is the structured outcome of a single check. The shape is
// stable for `--output json`.
type CheckResult struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Skipped  bool   `json:"skipped"`
	ExitCode int    `json:"exit_code"`
	Hint     string `json:"hint,omitempty"`
}

// Check is one diagnostic step. Name and ExitCode are static metadata so
// the runner can synthesise a Skipped result without invoking Run when a
// previous check has already failed. Run receives the resolved service
// account and an HTTP client (so tests inject a RoundTripper and the
// future per-package check can hit the real API).
type Check struct {
	Name     string
	ExitCode int
	Run      func(ctx context.Context, sa *serviceaccount.ServiceAccount, httpClient *http.Client) CheckResult
}

// Run executes checks in order. As soon as one check fails, every
// remaining check is reported as Skipped=true / Passed=false with its
// declared Name and ExitCode but no work done. A nil or empty chain
// returns nil.
//
// httpClient may be nil; checks that need network construct or wrap it
// themselves (typically by passing oauth2.HTTPClient through ctx).
func Run(ctx context.Context, sa *serviceaccount.ServiceAccount, httpClient *http.Client, checks ...Check) []CheckResult {
	if len(checks) == 0 {
		return nil
	}
	out := make([]CheckResult, 0, len(checks))
	failed := false
	for _, c := range checks {
		if failed {
			out = append(out, CheckResult{
				Name:     c.Name,
				Passed:   false,
				Skipped:  true,
				ExitCode: c.ExitCode,
			})
			continue
		}
		r := c.Run(ctx, sa, httpClient)
		// Name and ExitCode are owned by the check definition; the runner
		// guarantees the result carries them even if the check forgot.
		if r.Name == "" {
			r.Name = c.Name
		}
		if r.ExitCode == 0 && !r.Passed {
			r.ExitCode = c.ExitCode
		}
		out = append(out, r)
		if !r.Passed {
			failed = true
		}
	}
	return out
}
