package vitals_test

import (
	"errors"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

func TestParsers_undecodable2xxBody_exit30(t *testing.T) {
	const pkg = "com.example.app"
	cases := map[string]func() error{
		"anomalies": func() error { _, err := vitals.ParseAnomalies(pkg, []byte(`{"anomalies":"x"}`)); return err },
		"releases":  func() error { _, err := vitals.ParseReleases(pkg, []byte(`{"tracks":"x"}`)); return err },
		"timeline":  func() error { _, err := vitals.ParseTimeline(pkg, []byte(`{"rows":"x"}`)); return err },
		"freshness": func() error { _, err := vitals.ParseFreshness(pkg, []byte(`{"freshnessInfo":"x"}`)); return err },
		"issues":    func() error { _, err := vitals.ParseErrorIssues(pkg, []byte(`{"errorIssues":"x"}`)); return err },
		"reports":   func() error { _, err := vitals.ParseErrorReports(pkg, []byte(`{"errorReports":"x"}`)); return err },
	}
	for name, parse := range cases {
		t.Run(name, func(t *testing.T) {
			err := parse()
			if got := exit.For(err); got != 30 {
				t.Errorf("exit.For(err) = %d, want 30; err=%v", got, err)
			}
			var apiErr *api.Error
			if !errors.As(err, &apiErr) || api.NeverSent(apiErr.Cause) {
				t.Errorf("%v is not an answered *api.Error", err)
			}
		})
	}
}
