package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
)

const exitInterrupted = 50

type interruptError struct{ sig os.Signal }

func (e interruptError) Error() string { return "interrupted by " + signalName(e.sig) }

func signalName(sig os.Signal) string {
	switch sig {
	case os.Interrupt:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return sig.String()
}

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
