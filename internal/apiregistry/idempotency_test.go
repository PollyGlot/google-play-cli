package apiregistry_test

import (
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
)

// TestIdempotentBit pins the declared bit on the methods whose replay matters:
// the writes the audit (#575) caught being replayed, the read-shaped POSTs that
// must keep --retry, and the verb defaults.
func TestIdempotentBit(t *testing.T) {
	cases := map[string]bool{
		// Non-idempotent: creates, appends and one-shot actions.
		"androidpublisher.edits.images.upload":                   false,
		"androidpublisher.edits.commit":                          false,
		"androidpublisher.orders.refund":                         false,
		"androidpublisher.apprecovery.create":                    false,
		"androidpublisher.apprecovery.deploy":                    false,
		"androidpublisher.appsigning.rotateAppSigningKey":        false,
		"androidpublisher.users.create":                          false,
		"androidpublisher.grants.create":                         false,
		"gamesConfiguration.achievementConfigurations.insert":    false,
		"androidpublisher.internalappsharingartifacts.uploadapk": false,
		// Replay-safe POSTs.
		"androidpublisher.edits.insert":                     true,
		"androidpublisher.edits.validate":                   true,
		"androidpublisher.monetization.convertRegionPrices": true,
		"playdeveloperreporting.vitals.crashrate.query":     true,
		// Verb defaults.
		"androidpublisher.edits.tracks.get":                   true,
		"androidpublisher.edits.tracks.update":                true,
		"androidpublisher.edits.images.delete":                true,
		"androidpublisher.monetization.onetimeproducts.patch": true,
	}
	for id, want := range cases {
		m, err := apiregistry.Resolve(id)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", id, err)
		}
		if m.Idempotent != want {
			t.Errorf("%s (%s).Idempotent = %v, want %v", id, m.Verb, m.Idempotent, want)
		}
	}
}

// TestPaginatedBit checks the bit is read from the response schema: the
// query-param lists, the legacy tokenPagination envelope, and the vitals
// queries whose token rides the body are paginated; a single-resource read is
// not.
func TestPaginatedBit(t *testing.T) {
	cases := map[string]bool{
		"androidpublisher.monetization.onetimeproducts.list": true,
		"androidpublisher.inappproducts.list":                true,
		"playdeveloperreporting.vitals.crashrate.query":      true,
		"gamesConfiguration.leaderboardConfigurations.list":  true,
		"androidpublisher.edits.tracks.list":                 false,
		"androidpublisher.orders.get":                        false,
		"androidpublisher.edits.insert":                      false,
	}
	for id, want := range cases {
		m, err := apiregistry.Resolve(id)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", id, err)
		}
		if m.Paginated != want {
			t.Errorf("%s.Paginated = %v, want %v", id, m.Paginated, want)
		}
	}
}

// TestIdempotentRequest covers the classifier the retry transport calls: a
// request built from the registry carries its method's bit, and one that
// matches no registered method falls back to its verb.
func TestIdempotentRequest(t *testing.T) {
	upload := apiregistry.MustResolve("androidpublisher.edits.images.upload")
	uploadURL, err := upload.UploadURL(map[string]string{"packageName": "com.example.app", "editId": "e1", "language": "en-US", "imageType": "phoneScreenshots"})
	if err != nil {
		t.Fatal(err)
	}
	validate := apiregistry.MustResolve("androidpublisher.edits.validate")
	validateURL, err := validate.URL(map[string]string{"packageName": "com.example.app", "editId": "e1"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		verb, url string
		want      bool
	}{
		{http.MethodPost, uploadURL + "?uploadType=media", false},
		{http.MethodPost, validateURL, true},
		{http.MethodGet, "https://storage.googleapis.com/storage/v1/b/bucket/o", true},
		{http.MethodPost, "https://example.test/unknown", false},
		{http.MethodPut, "https://example.test/unknown", true},
	}
	for _, tc := range cases {
		req, err := http.NewRequestWithContext(t.Context(), tc.verb, tc.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := apiregistry.IdempotentRequest(req); got != tc.want {
			t.Errorf("IdempotentRequest(%s %s) = %v, want %v", tc.verb, tc.url, got, tc.want)
		}
	}
}
