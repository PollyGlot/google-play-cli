package doctor_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/redact"
)

type leakingKeyring struct{}

const leakedKeyringError = "keyring read failed: -----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgLEAKEDSECRETBODY\n-----END PRIVATE KEY-----" +
	" (Authorization: Bearer ya29.a0AfB_byLEAKEDSECRETBODY123456)"

func (leakingKeyring) Set(string, string, string) error { return nil }
func (leakingKeyring) Delete(string, string) error      { return nil }
func (leakingKeyring) Get(string, string) (string, error) {
	return "", errors.New(leakedKeyringError)
}

func TestDoctor_hintsAreRedactedOnStdout(t *testing.T) {
	for _, format := range []string{"json", "table", "markdown"} {
		t.Run(format, func(t *testing.T) {
			boot := newBoot(t)
			boot.Keyring = leakingKeyring{}
			cfg := &config.Global{}
			cfg.AddAccount("playci")
			if err := cfg.SetActive("playci"); err != nil {
				t.Fatalf("SetActive: %v", err)
			}
			if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
				t.Fatalf("cfg.Save: %v", err)
			}

			var stdout, stderr bytes.Buffer
			runErr := runCmd(t, boot, ctxWithRT(successRT()), &stdout, &stderr, "--output", format)
			if got := exit.For(runErr); got != 10 {
				t.Fatalf("exit.For(err) = %d, want 10 (err=%v)", got, runErr)
			}
			out := stdout.String()
			for _, marker := range []string{"LEAKEDSECRETBODY", "BEGIN PRIVATE KEY", "ya29."} {
				if strings.Contains(out, marker) {
					t.Errorf("stdout leaks %q:\n%s", marker, out)
				}
			}
			for _, want := range []string{"keyring read failed", redact.Mask} {
				if !strings.Contains(out, want) {
					t.Errorf("stdout lost %q:\n%s", want, out)
				}
			}
		})
	}
}
