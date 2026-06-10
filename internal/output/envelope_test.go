package output_test

import (
	"bytes"
	"encoding/json"
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
