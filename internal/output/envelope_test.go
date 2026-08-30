package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// decodeEnvelope parses one ErrorEnvelope out of buf and fails the test if buf
// is not exactly one well-formed JSON object (no trailing second object).
func decodeEnvelope(t *testing.T, buf *bytes.Buffer) output.ErrorEnvelope {
	t.Helper()
	dec := json.NewDecoder(buf)
	var env output.ErrorEnvelope
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a well-formed JSON envelope: %v\n%s", err, buf.String())
	}
	if dec.More() {
		t.Fatalf("stdout carried more than one JSON value:\n%s", buf.String())
	}
	return env
}

func TestWriteErrorEnvelope_apiError_carriesExitCodeAndReasons(t *testing.T) {
	err := &api.Error{
		Operation:  "edits.commit",
		Package:    "com.example.app",
		StatusCode: 409,
		Message:    "edit already exists",
		Reasons:    []string{"editAlreadyExists"},
	}
	var buf bytes.Buffer
	if werr := output.WriteErrorEnvelope(&buf, err); werr != nil {
		t.Fatalf("WriteErrorEnvelope: %v", werr)
	}
	env := decodeEnvelope(t, &buf)
	if env.Error.ExitCode != 60 {
		t.Errorf("exitCode = %d, want 60 (409 conflict)", env.Error.ExitCode)
	}
	if len(env.Error.Reasons) != 1 || env.Error.Reasons[0] != "editAlreadyExists" {
		t.Errorf("reasons = %v, want [editAlreadyExists]", env.Error.Reasons)
	}
	if len(env.Error.Requires) != 0 {
		t.Errorf("requires = %v, want empty for a non-safety error", env.Error.Requires)
	}
	if env.Error.Message == "" {
		t.Errorf("message should carry the human error string")
	}
	if env.Error.Code != string(exit.CodeEditAlreadyExists) {
		t.Errorf("code = %q, want %q", env.Error.Code, exit.CodeEditAlreadyExists)
	}
	if env.Error.Retryable {
		t.Error("an already-open Edit is not fixed by replaying the same command")
	}
	if env.Error.Operation != "edits.commit" || env.Error.Package != "com.example.app" {
		t.Errorf("operation/package = %q/%q, want edits.commit/com.example.app",
			env.Error.Operation, env.Error.Package)
	}
}

// TestWriteErrorEnvelope_retryableIsAlwaysEmitted guards the one field where an
// omission and a false would be indistinguishable to a consumer: `retryable`
// must appear in the JSON even when it is false.
func TestWriteErrorEnvelope_retryableIsAlwaysEmitted(t *testing.T) {
	var buf bytes.Buffer
	if werr := output.WriteErrorEnvelope(&buf, exit.Usagef("missing --to")); werr != nil {
		t.Fatalf("WriteErrorEnvelope: %v", werr)
	}
	raw := buf.String()
	if !strings.Contains(raw, `"retryable"`) {
		t.Errorf("envelope omits retryable:\n%s", raw)
	}
	if !strings.Contains(raw, `"code"`) {
		t.Errorf("envelope omits code:\n%s", raw)
	}
	// A local failure made no API call, so neither field should be serialized.
	if strings.Contains(raw, `"operation"`) || strings.Contains(raw, `"package"`) {
		t.Errorf("a local failure must not claim an operation or package:\n%s", raw)
	}
}

func TestWriteErrorEnvelope_retryableUpstreamFailure(t *testing.T) {
	err := &api.Error{Operation: "edits.commit", Package: "com.example.app", StatusCode: 503, Message: "backend error"}
	var buf bytes.Buffer
	if werr := output.WriteErrorEnvelope(&buf, err); werr != nil {
		t.Fatalf("WriteErrorEnvelope: %v", werr)
	}
	env := decodeEnvelope(t, &buf)
	if env.Error.Code != string(exit.CodeUpstreamUnavailable) {
		t.Errorf("code = %q, want %q", env.Error.Code, exit.CodeUpstreamUnavailable)
	}
	if !env.Error.Retryable {
		t.Error("a 5xx must be marked retryable so a wrapper needs no cause table")
	}
}

func TestWriteErrorEnvelope_safetyRefusal_carriesRequires(t *testing.T) {
	err := exit.SafetyFlag("confirm", "removing a user is destructive; pass --confirm")
	var buf bytes.Buffer
	if werr := output.WriteErrorEnvelope(&buf, err); werr != nil {
		t.Fatalf("WriteErrorEnvelope: %v", werr)
	}
	env := decodeEnvelope(t, &buf)
	if env.Error.ExitCode != 3 {
		t.Errorf("exitCode = %d, want 3 (safety flag required)", env.Error.ExitCode)
	}
	if len(env.Error.Requires) != 1 || env.Error.Requires[0] != "confirm" {
		t.Errorf("requires = %v, want [confirm]", env.Error.Requires)
	}
	if len(env.Error.Reasons) != 0 {
		t.Errorf("reasons = %v, want empty for a safety refusal", env.Error.Reasons)
	}
}

func TestWriteErrorEnvelope_usageError_codeOnly(t *testing.T) {
	err := exit.Usagef("unknown column %q", "bogus")
	var buf bytes.Buffer
	if werr := output.WriteErrorEnvelope(&buf, err); werr != nil {
		t.Fatalf("WriteErrorEnvelope: %v", werr)
	}
	env := decodeEnvelope(t, &buf)
	if env.Error.ExitCode != 2 {
		t.Errorf("exitCode = %d, want 2 (CLI misuse)", env.Error.ExitCode)
	}
	if len(env.Error.Reasons) != 0 || len(env.Error.Requires) != 0 {
		t.Errorf("usage error should carry neither reasons nor requires; got reasons=%v requires=%v",
			env.Error.Reasons, env.Error.Requires)
	}
}
