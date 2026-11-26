package details_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
)

func TestGet_nonJSONBody_failsAtItsFetchNotAtTheEnvelope(t *testing.T) {
	cases := []struct {
		name    string
		details string
		listing string
		wantOp  string
	}{
		{"details body", `not json`, `{"title":"App"}`, "details.get"},
		{"listing body", `{"defaultLanguage":"en-US"}`, `not json`, "listings.get"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &infoRT{t: t, editID: "edit-nonjson", details: tc.details, listing: tc.listing, detailsCode: 200, listingCode: 200, iconCode: 200}
			_, _, err := details.Get(context.Background(), rt.client(), "com.example.app")
			var apiErr *api.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *api.Error in the chain", err)
			}
			if apiErr.Operation != tc.wantOp {
				t.Errorf("api.Error.Operation = %q, want %q: a body that is not JSON must never reach the envelope", apiErr.Operation, tc.wantOp)
			}
			if code := exitCodeOf(t, err); code != 30 {
				t.Errorf("ExitCode() = %d, want 30", code)
			}
		})
	}
}
