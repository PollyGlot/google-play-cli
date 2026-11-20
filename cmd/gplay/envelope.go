package main

import (
	"io"
	"os"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/output"
)

type stdoutTally struct {
	f *os.File
	n int64
}

func (t *stdoutTally) Write(p []byte) (int, error) {
	n, err := t.f.Write(p)
	t.n += int64(n)
	return n, err
}

func (t *stdoutTally) Fd() uintptr { return t.f.Fd() }

func writeFailureEnvelope(stdout io.Writer, wrote bool, argv []string, err error) {
	if err == nil || wrote {
		return
	}
	format, ferr := output.Resolve(output.Format(outputFlagValue(argv)), stdout)
	if ferr != nil || format != output.FormatJSON {
		return
	}
	_ = output.WriteErrorEnvelope(stdout, err)
}

func outputFlagValue(argv []string) string {
	for i, a := range argv {
		if a == "--" {
			break
		}
		if v, ok := strings.CutPrefix(a, "--output="); ok {
			return v
		}
		if a == "--output" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}
