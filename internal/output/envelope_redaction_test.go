package output_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/redact"
)

// The envelope's message is gplay's own error text, written to STDOUT where the
// stderr filter does not reach (ADR-0023). A wrapped error that quotes a
// credential must come out masked there too (#583).
func TestWriteErrorEnvelope_messageIsRedacted(t *testing.T) {
	leaky := errors.New(`could not read credential: invalid key in {"private_key_id":"kid-LEAKEDSECRETBODY","private_key":"-----BEGIN PRIVATE KEY-----\nMIIEvQLEAKEDSECRETBODY\n-----END PRIVATE KEY-----\n"}`)

	var buf bytes.Buffer
	if err := output.WriteErrorEnvelope(&buf, leaky); err != nil {
		t.Fatalf("WriteErrorEnvelope: %v", err)
	}
	raw := buf.String()
	for _, marker := range []string{"LEAKEDSECRETBODY", "BEGIN PRIVATE KEY"} {
		if strings.Contains(raw, marker) {
			t.Errorf("envelope leaks %q:\n%s", marker, raw)
		}
	}
	env := decodeEnvelope(t, &buf)
	if !strings.HasPrefix(env.Error.Message, "could not read credential") || !strings.Contains(env.Error.Message, redact.Mask) {
		t.Errorf("message = %q, want the diagnostic kept and the secret masked", env.Error.Message)
	}
}
