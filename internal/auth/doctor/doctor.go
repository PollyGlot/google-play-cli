package doctor

import (
	"context"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
)

type CheckResult struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Skipped  bool   `json:"skipped"`
	ExitCode int    `json:"exit_code"`
	Hint     string `json:"hint,omitempty"`
}

type Check struct {
	Name     string
	ExitCode int
	Run      func(ctx context.Context, sa *serviceaccount.ServiceAccount, httpClient *http.Client) CheckResult
}

func NewSkippedResult(c Check) CheckResult {
	return CheckResult{
		Name:     c.Name,
		Passed:   false,
		Skipped:  true,
		ExitCode: c.ExitCode,
	}
}

func Run(ctx context.Context, sa *serviceaccount.ServiceAccount, httpClient *http.Client, checks ...Check) []CheckResult {
	if len(checks) == 0 {
		return nil
	}
	out := make([]CheckResult, 0, len(checks))
	failed := false
	for _, c := range checks {
		if failed {
			out = append(out, NewSkippedResult(c))
			continue
		}
		r := c.Run(ctx, sa, httpClient)
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
