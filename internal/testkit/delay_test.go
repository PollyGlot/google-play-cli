package testkit_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// Delayed must count overlap, not calls: three requests held open together
// peak at 3, and the fake still sees every one of them.
func TestDelayed_recordsPeakInFlight(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(200, `{}`))
	d := testkit.NewDelayed(fake, func(*http.Request) time.Duration { return 30 * time.Millisecond })
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/x", nil)
			if resp, err := d.RoundTrip(req); err == nil {
				_ = resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	if d.Peak() != 3 || d.Requests() != 3 || len(fake.Calls()) != 3 {
		t.Errorf("peak=%d requests=%d calls=%d, want 3/3/3", d.Peak(), d.Requests(), len(fake.Calls()))
	}
}
