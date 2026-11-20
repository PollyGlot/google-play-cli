package kernel

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

type Logger struct {
	w io.Writer
}

func LoggerFor(cmd *cobra.Command) Logger {
	return Logger{w: cmd.ErrOrStderr()}
}

func (rc *RunContext) log() Logger { return Logger{w: rc.Stderr} }

func (l Logger) emit(prefix, format string, args ...any) {
	if l.w == nil {
		return
	}
	_, _ = fmt.Fprintf(l.w, prefix+format+"\n", args...)
}

func (l Logger) Confirmf(format string, args ...any) { l.emit("✓ ", format, args...) }

func (l Logger) Warnf(format string, args ...any) { l.emit("warning: ", format, args...) }

func (l Logger) Notef(format string, args ...any) { l.emit("NOTE: ", format, args...) }

func (l Logger) Logf(format string, args ...any) { l.emit("", format, args...) }

func (l Logger) Failf(format string, args ...any) { l.emit("", format, args...) }

func (rc *RunContext) Notef(format string, args ...any) { rc.log().Notef(format, args...) }

func (rc *RunContext) Logf(format string, args ...any) { rc.log().Logf(format, args...) }

func (rc *RunContext) Failf(format string, args ...any) { rc.log().Failf(format, args...) }
