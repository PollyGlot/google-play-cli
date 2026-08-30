// Package signingcmd holds the wiring shared by the `gplay signing` leaves:
// package resolution, PEM/lineage file reading, the --confirm gate, the
// certificate-hash table (ADR-0018) and 404/403 hint classification. Mirrors
// commands/recovery/recoverycmd.
package signingcmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// Usagef builds a usage error (exit 2).
func Usagef(format string, a ...any) error { return exit.Usagef(format, a...) }

// ResolvePackage resolves the target app: --package wins, else the project pin.
// The API calls this path parameter `name` and accepts either the Android
// package name or the app ID; gplay keeps its own vocabulary (Project pin,
// --package) so the flag matches every other command.
func ResolvePackage(rc *kernel.RunContext, flag string) (string, error) {
	pkg := strings.TrimSpace(flag)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return "", Usagef("no package: pass --package <pkg> or run gplay init in your repo")
	}
	return pkg, nil
}

// RequireConfirm enforces the DESTRUCTIVE-tier --confirm gate (ADR-0017/0043):
// both signing leaves change the live signing key of a real app, which is
// irreversible and externally visible, so they refuse with exit 3 (a
// deterministically resolvable *exit.SafetyFlagError naming the flag) unless
// --confirm is passed. The LIVE path calls this; --dry-run never does (it
// reports the requirement through the payload's `requires` array instead).
func RequireConfirm(confirm bool, msg string) error {
	if confirm {
		return nil
	}
	return exit.SafetyFlag("confirm", "%s", msg)
}

// ReadPEM reads a certificate file and hands back its raw bytes (the caller
// passes them straight to the API, which base64-encodes them as a `format:
// byte` field). Taking a path rather than a blob is deliberate: nobody should
// have to paste base64 on a command line. A file that is not PEM is caught here
// instead of a hundred milliseconds later as an opaque 400.
func ReadPEM(flag, path string) ([]byte, error) {
	b, err := ReadFile(flag, path)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(string(b), "-----BEGIN") {
		return nil, Usagef("--%s: %s is not a PEM certificate (no \"-----BEGIN\" header): export the certificate in PEM format", flag, path)
	}
	return b, nil
}

// ReadFile reads an input file, turning an unreadable path into a usage error
// naming the flag rather than a bare filesystem error.
func ReadFile(flag, path string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own flag value
	if err != nil {
		return nil, Usagef("--%s: cannot read %s: %v", flag, path, err)
	}
	if len(b) == 0 {
		return nil, Usagef("--%s: %s is empty", flag, path)
	}
	return b, nil
}

// packageNotFoundError / forbiddenError attach actionable hints, leaving the
// wrapped *api.Error to drive the exit code.
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("app %q not found: verify the package name with `gplay apps list` (appsigning also accepts an app ID): %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not allowed to manage app signing for %q: it needs app access in the Play Console (Setup → API access) AND the Cloud KMS key must grant Google Play the Decrypt and Sign permissions: %v", e.pkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// Classify adds 404/403 hints, leaving the *api.Error to drive the exit code.
func Classify(pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return &packageNotFoundError{pkg: pkg, cause: err}
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		}
	}
	return err
}
