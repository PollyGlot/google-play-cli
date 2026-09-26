package doctor_test

import (
	"context"
	"net/http"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire_packageAccess pins the edits.insert + edits.delete round trip of
// the package-access check and the result each answer yields, byte for byte:
// testdata/wire.golden was recorded before the move onto the executor (#586)
// and must not move with it.
func TestWire_packageAccess(t *testing.T) {
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	check := []testkit.Send{{Name: "CheckPackageAccess", Fn: func(hc *http.Client) (any, error) {
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, hc)
		return doctor.Run(ctx, sa, nil, doctor.CheckPackageAccess("com.example.app")), nil
	}}}
	byMethod := func(method string, status int, body string) testkit.Responder {
		return func(c testkit.Call) (int, string, bool) { return status, body, c.Method == method }
	}
	golden := testkit.Exchanges(check,
		testkit.Answer{Name: "ok", Responders: []testkit.Responder{testkit.Any(http.StatusOK, `{"id":"e 1/2","expiryTimeSeconds":"1"}`)}},
		testkit.Answer{Name: "created, delete 204", Responders: []testkit.Responder{
			byMethod(http.MethodPost, http.StatusCreated, `{"id":"e1"}`), byMethod(http.MethodDelete, http.StatusNoContent, ``),
		}},
		testkit.AnswerDenied,
		testkit.Answer{Name: "not found", Responders: []testkit.Responder{testkit.Any(http.StatusNotFound, `{"error":{"code":404,"message":"Package not found: com.example.app."}}`)}},
		testkit.Answer{Name: "bad request", Responders: []testkit.Responder{testkit.Any(http.StatusBadRequest, `  plain text refusal  `)}},
		testkit.Answer{Name: "unavailable", Responders: []testkit.Responder{testkit.Any(http.StatusServiceUnavailable, ``)}},
		testkit.Answer{Name: "unauthorized", Responders: []testkit.Responder{testkit.Any(http.StatusUnauthorized, ``)}},
		testkit.Answer{Name: "accepted", Responders: []testkit.Responder{testkit.Any(http.StatusAccepted, `{"id":"e1"}`)}},
		testkit.AnswerMalformed,
		testkit.Answer{Name: "no id", Responders: []testkit.Responder{testkit.Any(http.StatusOK, `{}`)}},
		testkit.Answer{Name: "delete fails", Responders: []testkit.Responder{
			byMethod(http.MethodPost, http.StatusOK, `{"id":"e1"}`),
			byMethod(http.MethodDelete, http.StatusConflict, `{"error":{"message":"edit is being committed"}}`),
		}},
		testkit.Answer{Name: "delete 5xx", Responders: []testkit.Responder{
			byMethod(http.MethodPost, http.StatusOK, `{"id":"e1"}`), byMethod(http.MethodDelete, http.StatusBadGateway, ``),
		}},
	)
	testkit.Golden(t, "wire.golden", []byte(golden))
}
