package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

type Error struct {
	Operation  string
	Package    string
	Resource   Resource
	StatusCode int
	Message    string
	Reasons    []string
	Cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	suffix := ""
	if len(e.Reasons) > 0 {
		suffix = " [reason: " + strings.Join(e.Reasons, ",") + "]"
	}
	on := e.Target().ID
	if e.StatusCode == 0 {
		return fmt.Sprintf("%s on %s: %s%s", e.Operation, on, e.Message, suffix)
	}
	return fmt.Sprintf("%s on %s: %s (HTTP %d)%s", e.Operation, on, e.Message, e.StatusCode, suffix)
}

func (e *Error) Unwrap() error { return e.Cause }

func (e *Error) ExitCode() int {
	if e == nil {
		return 0
	}
	if isBodyRead(e.Cause) {
		return StatusToExitCode(0)
	}
	if e.StatusCode == 0 {
		var coder interface{ ExitCode() int }
		if errors.As(e.Cause, &coder) {
			return coder.ExitCode()
		}
	}
	if e.Operation == "bundles.upload" || e.Operation == "apks.upload" {
		switch e.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound:
			return 20
		}
	}
	return StatusToExitCode(e.StatusCode)
}

func DecodeFailure(e Error) *Error {
	if e.StatusCode == 0 {
		e.StatusCode = http.StatusOK
	}
	return &e
}

type notSentError struct {
	err   error
	local bool
}

func (e *notSentError) Error() string { return e.err.Error() }
func (e *notSentError) Unwrap() error { return e.err }

func (e *notSentError) ExitCode() int {
	if e.local {
		return 20
	}
	return 1
}

func NeverSent(cause error) bool {
	var coder interface{ ExitCode() int }
	if errors.As(cause, &coder) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(cause, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(cause, &opErr) && opErr.Op == "dial"
}

func StatusToExitCode(status int) int {
	switch {
	case status == 0:
		return 50
	case status == http.StatusUnauthorized:
		return 10
	case status == http.StatusForbidden:
		return 11
	case status == http.StatusConflict, status == http.StatusTooManyRequests:
		return 60
	case status >= 500:
		return 40
	default:
		return 30
	}
}

func APIErrorMessage(body []byte, status int) string {
	msg, _ := ParseErrorEnvelope(body, status)
	return msg
}

func ParseErrorEnvelope(body []byte, status int) (message string, reasons []string) {
	if len(body) == 0 {
		return fmt.Sprintf("HTTP %d", status), nil
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		message = env.Error.Message
		for _, e := range env.Error.Errors {
			if e.Reason != "" {
				reasons = append(reasons, e.Reason)
			}
		}
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
		if message == "" {
			message = fmt.Sprintf("HTTP %d", status)
		}
	}
	return message, reasons
}
