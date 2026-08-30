package kernel

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// leakyPEM is synthetic key material with an unmistakable canary body.
const leakyPEM = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgLEAKEDSECRETBODY\n-----END PRIVATE KEY-----"

// Credential redaction is a property of rc.Stderr itself (PRD #459 / slice
// #460), not a rule each Fprintf must remember. Whatever a command writes
// through the RunContext comes out masked.
func TestRunContextStderrRedacts(t *testing.T) {
	var buf bytes.Buffer
	rc := NewForTest(context.Background(), Boot{Stderr: &buf}, Inputs{})

	_, _ = fmt.Fprintf(rc.Stderr, "keystore: failed to parse %s\n", leakyPEM)

	got := buf.String()
	if strings.Contains(got, "LEAKEDSECRETBODY") {
		t.Fatalf("rc.Stderr leaked key material:\n%s", got)
	}
	if !strings.Contains(got, "keystore: failed to parse") {
		t.Errorf("rc.Stderr dropped the diagnostic:\n%s", got)
	}
}

// The kernel's own stderr helpers ride the same writer, so they inherit the
// guarantee rather than each re-implementing it.
func TestConfirmfRedacts(t *testing.T) {
	var buf bytes.Buffer
	rc := NewForTest(context.Background(), Boot{Stderr: &buf}, Inputs{})

	rc.Confirmf("token minted: %s", "ya29.a0AfB_byLEAKEDSECRETBODY123456")

	if got := buf.String(); strings.Contains(got, "LEAKEDSECRETBODY") {
		t.Fatalf("Confirmf leaked token material:\n%s", got)
	}
}

// Stdout must stay verbatim: it mirrors API responses byte for byte (ADR-0003),
// and redacting it would corrupt payloads a machine consumer parses.
func TestStdoutIsNotRedacted(t *testing.T) {
	var out, errBuf bytes.Buffer
	rc := NewForTest(context.Background(), Boot{Stdout: &out, Stderr: &errBuf}, Inputs{})

	// A store listing legitimately quoting a PEM-looking string in its body.
	payload := `{"fullDescription":"` + strings.ReplaceAll(leakyPEM, "\n", `\n`) + `"}`
	_, _ = fmt.Fprint(rc.Stdout, payload)

	if got := out.String(); got != payload {
		t.Errorf("stdout was rewritten; ADR-0003 requires verbatim pass-through:\ngot  %s\nwant %s", got, payload)
	}
}

// A nil Stderr on a hand-built Boot must stay a no-op, not a panic: the
// redaction wrapper absorbs the case the old io.Discard fallback covered.
func TestNilStderrStaysSafe(t *testing.T) {
	rc := NewForTest(context.Background(), Boot{}, Inputs{})
	if rc.Stderr == nil {
		t.Fatal("rc.Stderr is nil; every write site would panic")
	}
	rc.Confirmf("no writer configured")
}
