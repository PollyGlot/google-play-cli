package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/redact"
)

// echoSAJSON is a synthetic service account (never a real credential) whose
// key body and key id carry an unmistakable canary.
const echoSAJSON = `{
  "type": "service_account",
  "project_id": "fake-proj",
  "private_key_id": "kid-LEAKEDSECRETBODY",
  "private_key": "-----BEGIN PRIVATE KEY-----\nMIICdgIBADANBgkqhkiG9w0BLEAKEDSECRETBODY\n-----END PRIVATE KEY-----\n",
  "client_email": "fake@fake-proj.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

// TestCredentialValueNeverEchoed is the end-to-end form of the audit repro
// (#583, repro-credential-echo.sh): a credential in one of the shapes CI secret
// stores produce (base64, quoted JSON, BOM-prefixed JSON) used to come back
// whole in "could not read credential: open <value>: file name too long", on
// stderr AND in the stdout JSON envelope / doctor checklist. Every scenario
// runs the real command tree and greps BOTH streams, the way main wires them.
// Resolution fails before any request, so nothing here reaches the network.
func TestCredentialValueNeverEchoed(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte(echoSAJSON))
	values := map[string]string{
		"base64":      b64,
		"quoted JSON": `"` + echoSAJSON + `"`,
		"BOM JSON":    string(rune(0xFEFF)) + echoSAJSON,
	}
	commands := map[string][]string{
		"tracks list": {"tracks", "list", "--package", "com.example.app", "--output", "json"},
		"auth doctor": {"auth", "doctor", "--output", "json"},
	}
	for vname, value := range values {
		for cname, args := range commands {
			for _, via := range []string{"env", "flag"} {
				t.Run(vname+"/"+cname+"/"+via, func(t *testing.T) {
					dir := t.TempDir()
					t.Setenv(resolver.EnvAccount, "")
					argv := args
					if via == "env" {
						t.Setenv(resolver.EnvServiceAccount, value)
					} else {
						t.Setenv(resolver.EnvServiceAccount, "")
						argv = append(append([]string{}, args...), "--service-account", value)
					}

					var stdout, rawStderr bytes.Buffer
					stderr := redact.Writer(&rawStderr)
					root := newRootCmd(kernel.Boot{
						ConfigPath:   filepath.Join(dir, "config.json"),
						KeystoreRoot: filepath.Join(dir, "accounts"),
					})
					root.SetArgs(argv)
					root.SetOut(&stdout)
					root.SetErr(stderr)
					err := root.Execute()
					if err == nil {
						t.Fatal("expected a credential error, got nil")
					}
					// Exactly what main does with the error.
					_, _ = fmt.Fprintln(stderr, "gplay:", err)

					if code := exit.For(err); code != 10 {
						t.Errorf("exit code = %d, want 10 (auth)", code)
					}
					for stream, got := range map[string]string{"stdout": stdout.String(), "stderr": rawStderr.String()} {
						if leak := keyMaterialIn(got, value); leak != "" {
							t.Errorf("%s leaks key material (%q):\n%s", stream, leak, got)
						}
					}
					if !strings.Contains(stdout.String(), "value not shown") {
						t.Errorf("stdout does not carry the diagnostic:\n%s", stdout.String())
					}
				})
			}
		}
	}
}

// keyMaterialIn returns the first piece of credential material found in out:
// a PEM marker, the canary, or any 16-byte window of the raw value (which is
// what catches a base64 blob, invisible to every redaction pattern).
func keyMaterialIn(out, value string) string {
	for _, marker := range []string{"BEGIN PRIVATE KEY", "LEAKEDSECRETBODY"} {
		if strings.Contains(out, marker) {
			return marker
		}
	}
	for i := 0; i+16 <= len(value); i += 16 {
		if chunk := value[i : i+16]; strings.Contains(out, chunk) {
			return chunk
		}
	}
	return ""
}
