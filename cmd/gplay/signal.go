package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
)

// exitInterrupted is the exit code of a command stopped by SIGINT or SIGTERM:
// the network bucket of docs/DESIGN.md §9, retry-safe, because the implicit
// Edit was discarded on the way out and nothing was committed. It is the code
// a request cut by the canceled context already maps to; pinning it here keeps
// the outcome stable whichever step the signal lands on.
const exitInterrupted = 50

// interruptError is the cancel cause recording which signal stopped gplay.
type interruptError struct{ sig os.Signal }

func (e interruptError) Error() string { return "interrupted by " + signalName(e.sig) }

// signalName spells the two caught signals the way CI logs and docs do
// (Go's own String() says "interrupt" and "terminated").
func signalName(sig os.Signal) string {
	switch sig {
	case os.Interrupt:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return sig.String()
}

// notifyInterrupt derives a context canceled by the first SIGINT or SIGTERM.
// interrupted reports that signal (nil if none arrived); stop releases the
// handler and must be called once the command returns.
//
// Unlike signal.NotifyContext, it records the signal for the exit message and
// stops catching right after the first one, so the second signal gets the
// default action and kills gplay at once: the escape hatch when the cleanup
// itself hangs.
func notifyInterrupt(parent context.Context) (ctx context.Context, interrupted func() os.Signal, stop func()) {
	ctx, cancel := context.WithCancelCause(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			signal.Stop(ch)
			cancel(interruptError{sig: sig})
		case <-done:
		}
	}()
	interrupted = func() os.Signal {
		var ie interruptError
		if errors.As(context.Cause(ctx), &ie) {
			return ie.sig
		}
		return nil
	}
	stop = func() {
		signal.Stop(ch)
		close(done)
		cancel(nil)
	}
	return ctx, interrupted, stop
}
