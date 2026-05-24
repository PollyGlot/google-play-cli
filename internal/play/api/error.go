package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Error is the canonical error type returned by every internal/play/*
// module on an upstream failure. It carries the HTTP status (or 0 for
// transport-level failures), the operation name (e.g. "edits.insert"),
// the package the call targeted, and a human-readable message extracted
// from the API error envelope. It implements gplay's Coder contract so
// exit.For can map it to a semantic exit code without an explicit
// dispatcher.
type Error struct {
	Operation  string // e.g. "edits.insert", "bundles.upload"
	Package    string
	StatusCode int    // 0 = transport-level (no HTTP response)
	Message    string // extracted via APIErrorMessage when a body is available
	Cause      error  // underlying transport error, if any
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.StatusCode == 0 {
		return fmt.Sprintf("%s on %s: %s", e.Operation, e.Package, e.Message)
	}
	return fmt.Sprintf("%s on %s: %s (HTTP %d)", e.Operation, e.Package, e.Message, e.StatusCode)
}

// Unwrap exposes the transport-level cause (if any) so errors.Is /
// errors.As traverse the chain naturally.
func (e *Error) Unwrap() error { return e.Cause }

// ExitCode satisfies the gplay Coder contract. The mapping table mostly
// lives in StatusToExitCode and matches docs/DESIGN.md §9; the one
// operation-aware nuance handled here is bundles.upload, where 400 and
// 404 mean "malformed AAB" and must surface as exit 20 (not the generic
// 30). Auth (403), conflict (409), 5xx and transport-level failures
// still win over the operation hint — a 403 on bundles.upload is still
// an auth problem.
func (e *Error) ExitCode() int {
	if e == nil {
		return 0
	}
	if e.Operation == "bundles.upload" {
		switch e.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound:
			return 20
		}
	}
	return StatusToExitCode(e.StatusCode)
}

// StatusToExitCode maps a Google Play API HTTP status (or 0 for
// transport failures) to the gplay exit-code taxonomy. It is the
// operation-agnostic fallback used by *Error.ExitCode; operation-aware
// overrides (e.g. bundles.upload 400/404 → 20) live on the receiver.
// The full table is in docs/DESIGN.md §9; the cases that matter:
//
//	0           → 50 (transport: timeout, DNS, refused)
//	403         → 11 (authorization — SA not invited on app)
//	409         → 60 (state conflict — stale Edit, ambiguous target)
//	429         → 60 (rate-limited — same "transient state, sometimes retry" bucket)
//	5xx         → 40 (upstream temporarily unhealthy, retry-safe)
//	other 4xx   → 30 (API misuse: not found, bad request, conflict, gone)
func StatusToExitCode(status int) int {
	switch {
	case status == 0:
		return 50
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

// APIErrorMessage extracts the most useful human-readable message from
// a Google API error envelope ({"error":{"message":"..."}}), falling
// back to a generic placeholder when the body is empty or unparseable.
func APIErrorMessage(body []byte, status int) string {
	if len(body) == 0 {
		return fmt.Sprintf("HTTP %d", status)
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		return env.Error.Message
	}
	return strings.TrimSpace(string(body))
}
