package serviceaccount_test

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
)

// osReader is the production FileReader shape, local to the test so this
// package stays import-free of internal/config.
type osReader struct{}

func (osReader) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// leakySAJSON carries a synthetic key whose body is an unmistakable canary: a
// leak shows up as LEAKEDSECRETBODY in a failing assertion.
const leakySAJSON = `{
  "type": "service_account",
  "project_id": "p",
  "private_key_id": "kid-LEAKEDSECRETBODY",
  "private_key": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BLEAKEDSECRETBODY\n-----END PRIVATE KEY-----\n",
  "client_email": "ci@p.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

// misshapenValues are the credential shapes that CI secret stores and YAML
// produce and that fall through to the path branch (#583): each one used to
// come back verbatim inside "open <value>: file name too long".
func misshapenValues() map[string]string {
	return map[string]string{
		"base64":              base64.StdEncoding.EncodeToString([]byte(leakySAJSON)),
		"base64 line-wrapped": wrap(base64.StdEncoding.EncodeToString([]byte(leakySAJSON)), 76),
		"double-quoted JSON":  `"` + leakySAJSON + `"`,
		"single-quoted JSON":  `'` + leakySAJSON + `'`,
		"BOM-prefixed JSON":   "\uFEFF" + leakySAJSON,
	}
}

func wrap(s string, n int) string {
	var b strings.Builder
	for len(s) > n {
		b.WriteString(s[:n] + "\n")
		s = s[n:]
	}
	b.WriteString(s)
	return b.String()
}

// secretChunks slices value into 16-byte windows: none may survive into the
// error, which is how a base64 blob (no PEM marker, no canary word) is caught.
func secretChunks(value string) []string {
	var out []string
	for i := 0; i+16 <= len(value); i += 16 {
		out = append(out, value[i:i+16])
	}
	return out
}

func TestLoadValue_misshapenValue_neverEchoesIt(t *testing.T) {
	wantHint := map[string]string{
		"base64":              "base64-encoded",
		"base64 line-wrapped": "base64-encoded",
		"double-quoted JSON":  "wrapped in quotes",
		"single-quoted JSON":  "wrapped in quotes",
		"BOM-prefixed JSON":   "byte-order mark",
	}
	for name, value := range misshapenValues() {
		t.Run(name, func(t *testing.T) {
			_, err := serviceaccount.LoadValue(osReader{}, "GPLAY_SERVICE_ACCOUNT", value)
			if err == nil {
				t.Fatal("LoadValue accepted a misshapen value")
			}
			msg := err.Error()
			for _, marker := range []string{"BEGIN PRIVATE KEY", "LEAKEDSECRETBODY"} {
				if strings.Contains(msg, marker) {
					t.Errorf("error leaks %q:\n%s", marker, msg)
				}
			}
			for _, chunk := range secretChunks(value) {
				if strings.Contains(msg, chunk) {
					t.Fatalf("error echoes the value (chunk %q):\n%s", chunk, msg)
				}
			}
			var uve *serviceaccount.UnreadableValueError
			if !errors.As(err, &uve) {
				t.Fatalf("error is %T, want *UnreadableValueError", err)
			}
			if uve.ExitCode() != 10 {
				t.Errorf("ExitCode = %d, want 10 (auth)", uve.ExitCode())
			}
			for _, want := range []string{"GPLAY_SERVICE_ACCOUNT", "value not shown", wantHint[name]} {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q missing %q", msg, want)
				}
			}
		})
	}
}

// A short missing path cannot be a credential, so it is named back: a typo'd
// path must stay diagnosable.
func TestLoadValue_shortMissingPath_isShown(t *testing.T) {
	path := t.TempDir() + "/missing-sa.json"
	_, err := serviceaccount.LoadValue(osReader{}, "--service-account", path)
	if err == nil {
		t.Fatal("expected an error for a missing path")
	}
	msg := err.Error()
	for _, want := range []string{"--service-account", path, "no such file or directory", "starting with '{'"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	if strings.Contains(msg, "value not shown") {
		t.Errorf("a short path should be shown: %s", msg)
	}
}

// Anything that could carry key material stays hidden, whatever else it looks
// like: too long, multi-line, holding a '{' or a PEM marker.
func TestLoadValue_credentialLikeValue_isNeverShown(t *testing.T) {
	for name, value := range map[string]string{
		"long single line": "/tmp/" + strings.Repeat("LEAKEDSECRETBODY", 20),
		"multi-line":       "/tmp/a\nLEAKEDSECRETBODY",
		"brace":            "x{LEAKEDSECRETBODY",
		"PEM marker":       "-----BEGIN PRIVATE KEY-----LEAKEDSECRETBODY",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := serviceaccount.LoadValue(osReader{}, "GPLAY_SERVICE_ACCOUNT", value)
			if err == nil {
				t.Fatal("expected an error")
			}
			msg := err.Error()
			if strings.Contains(msg, "LEAKEDSECRETBODY") {
				t.Errorf("error echoes the value: %s", msg)
			}
			if !strings.Contains(msg, "value not shown") {
				t.Errorf("error %q should say the value is not shown", msg)
			}
		})
	}
}

func TestLoadValue_inlineJSONAndPath_stillLoad(t *testing.T) {
	inline, err := serviceaccount.LoadValue(osReader{}, "--service-account", "  \n"+validSAJSON)
	if err != nil {
		t.Fatalf("inline JSON: %v", err)
	}
	fromFile, err := serviceaccount.LoadValue(osReader{}, "--service-account", writeTempJSON(t, validSAJSON))
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if inline.ClientEmail != fromFile.ClientEmail || inline.ClientEmail == "" {
		t.Errorf("ClientEmail inline=%q file=%q", inline.ClientEmail, fromFile.ClientEmail)
	}
}
