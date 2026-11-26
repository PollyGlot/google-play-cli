package doctor_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestDoctor_emptyPackageValue_isUsageError_beforeAnyCheck(t *testing.T) {
	boot := newBoot(t)
	seedActiveAccount(t, boot, signedSAJSON(t))
	fake := testkit.NewFake()

	var stdout, stderr bytes.Buffer
	err := runCmd(t, boot, ctxWithRT(fake), &stdout, &stderr,
		"--output", "json", "--package", "com.example.app,,com.example.other")
	if got := exit.For(err); got != 2 {
		t.Fatalf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if msg := err.Error(); !strings.Contains(msg, "--package") || !strings.Contains(msg, "empty") {
		t.Errorf("error %q does not name the empty --package value", msg)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("checks ran before the usage error: %+v", calls)
	}
	if stdout.Len() != 0 {
		t.Errorf("check results printed before the usage error:\n%s", stdout.String())
	}
}
