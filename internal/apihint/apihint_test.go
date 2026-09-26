package apihint_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

func apiErr(status int) error {
	return fmt.Errorf("open edit: %w", &api.Error{Operation: "edits.insert", Package: "com.x", StatusCode: status, Message: "boom"})
}

// TestForPackage asserts the classifier maps 404/403 to the canonical hints,
// keeps the *api.Error reachable (so the exit code is unchanged), and passes
// every other failure through untouched.
func TestForPackage(t *testing.T) {
	t.Run("404", func(t *testing.T) {
		err := apihint.ForPackage("com.x", apiErr(404))
		var nf *apihint.PackageNotFoundError
		if !errors.As(err, &nf) || nf.Package != "com.x" {
			t.Fatalf("error = %T %v, want *PackageNotFoundError for com.x", err, err)
		}
		if !strings.Contains(err.Error(), "gplay apps list") {
			t.Errorf("hint missing from %q", err)
		}
		if got, want := exit.For(err), exit.For(apiErr(404)); got != want {
			t.Errorf("exit.For = %d, want the api.Error's %d", got, want)
		}
	})
	t.Run("403", func(t *testing.T) {
		err := apihint.ForPackage("com.x", apiErr(403))
		var fe *apihint.ForbiddenError
		if !errors.As(err, &fe) || fe.Package != "com.x" {
			t.Fatalf("error = %T %v, want *ForbiddenError for com.x", err, err)
		}
		if !strings.Contains(err.Error(), "Setup → API access") {
			t.Errorf("hint missing from %q", err)
		}
		if got, want := exit.For(err), exit.For(apiErr(403)); got != want {
			t.Errorf("exit.For = %d, want the api.Error's %d", got, want)
		}
	})
	t.Run("passthrough", func(t *testing.T) {
		for _, err := range []error{apiErr(500), errors.New("local"), nil} {
			if got := apihint.ForPackage("com.x", err); got != err {
				t.Errorf("ForPackage(%v) = %v, want it untouched", err, got)
			}
		}
	})
}

func TestForbidden_onlyWraps403(t *testing.T) {
	var fe *apihint.ForbiddenError
	if err := apihint.Forbidden("com.x", apiErr(403)); !errors.As(err, &fe) {
		t.Errorf("403: error = %T, want *ForbiddenError", err)
	}
	if in := apiErr(404); apihint.Forbidden("com.x", in) != in {
		t.Error("404 must pass through Forbidden untouched")
	}
}

func TestStatus(t *testing.T) {
	if got := apihint.Status(apiErr(409)); got != 409 {
		t.Errorf("Status = %d, want 409", got)
	}
	if got := apihint.Status(errors.New("x")); got != 0 {
		t.Errorf("Status(non-api) = %d, want 0", got)
	}
}
