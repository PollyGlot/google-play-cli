package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
)

// Call is one request to a registered API method. The zero value of every
// field but Method is a valid default, so a plain read is
// `Call{Method: mGet, Params: ..., Target: pkg}`.
type Call struct {
	// Method is the registry entry to call, resolved once at package init with
	// apiregistry.MustResolve. It supplies the verb and the URL template.
	Method apiregistry.Method
	// Params fills the `{param}` placeholders of the template (path-escaped by
	// the registry; a missing or unknown key is an error).
	Params map[string]string
	// Query is appended as the query string when non-empty.
	Query url.Values
	// Body is the request payload: nil sends none; []byte and json.RawMessage
	// are sent verbatim (a catalog file must reach the API byte for byte);
	// a *Stream is re-opened per attempt; any other value is JSON-encoded.
	Body any
	// ContentType overrides the "application/json" sent with a Body.
	ContentType string
	// Media sends the call to the method's media-upload endpoint with
	// uploadType=media (the simple protocol; resumable uploads go through
	// ResumableUpload).
	Media bool
	// Op names the operation in the returned *Error. Empty means the method id
	// without its service prefix (`androidpublisher.orders.get` → `orders.get`);
	// set it where a module's historical tag differs.
	Op string
	// Target is what the call addresses, carried as Error.Package: the package
	// name, or the developer / application id of an account-scoped surface.
	Target string
}

// Stream is a request body read from its source rather than held in memory
// (an artifact on disk). Open runs once per attempt, so a transport-level
// replay re-reads the payload from the start.
type Stream struct {
	Open func() (io.ReadCloser, error)
	Size int64
}

// Do sends c and returns the 2xx body verbatim, for the ADR-0003 JSON
// pass-through. Every failure is an *Error, so exit.For maps it through the
// docs/DESIGN.md §9 taxonomy; see the package comment for the full contract.
func Do(ctx context.Context, hc *http.Client, c Call) (json.RawMessage, error) {
	op := c.op()
	req, err := c.request(ctx, op)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &Error{Operation: op, Package: c.Target, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// An error envelope is a few hundred bytes: the cap only bounds a hostile
		// server, and a truncated envelope still yields its HTTP status.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, MaxAPIErrorBodyRead))
		msg, reasons := ParseErrorEnvelope(b, resp.StatusCode)
		return nil, &Error{Operation: op, Package: c.Target, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	// Read one byte past the cap: reaching it proves the body was cut, and a
	// cut JSON document must never reach --output json as if it were whole.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxAPISuccessBodyRead+1))
	if err != nil {
		return nil, &Error{
			Operation: op, Package: c.Target, StatusCode: resp.StatusCode,
			Message: "read response body: " + err.Error(),
			Cause:   &bodyReadError{err: err},
		}
	}
	if len(raw) > MaxAPISuccessBodyRead {
		return nil, &Error{
			Operation: op, Package: c.Target, StatusCode: resp.StatusCode,
			Message: fmt.Sprintf("response body exceeds the %d-byte limit (%d MiB): refusing to truncate it", MaxAPISuccessBodyRead, MaxAPISuccessBodyRead>>20),
		}
	}
	return json.RawMessage(raw), nil
}

// DoJSON is Do followed by decoding the body into out (a pointer). It returns
// the verbatim body alongside, for the commands that pass it through.
func DoJSON(ctx context.Context, hc *http.Client, c Call, out any) (json.RawMessage, error) {
	raw, err := Do(ctx, hc, c)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		// No StatusCode: the tag every module used before the executor, so the
		// exit code of a malformed body is unchanged by migrating onto it.
		return nil, &Error{Operation: c.op(), Package: c.Target, Message: "decode response: " + err.Error(), Cause: err}
	}
	return raw, nil
}

// op resolves the operation tag of c.
func (c Call) op() string {
	if c.Op != "" {
		return c.Op
	}
	if i := strings.IndexByte(c.Method.ID, '.'); i >= 0 {
		return c.Method.ID[i+1:]
	}
	return c.Method.ID
}

// request builds the *http.Request for c. A failure here happens before
// anything is sent; it keeps the StatusCode-0 tag the modules always used.
func (c Call) request(ctx context.Context, op string) (*http.Request, error) {
	fail := func(msg string, err error) error {
		return &Error{Operation: op, Package: c.Target, Message: msg, Cause: err}
	}
	build := c.Method.URL
	if c.Media {
		build = c.Method.UploadURL
	}
	u, err := build(c.Params)
	if err != nil {
		return nil, fail(err.Error(), err)
	}
	q := c.Query
	if c.Media {
		q = url.Values{}
		for k, v := range c.Query {
			q[k] = v
		}
		q.Set("uploadType", "media")
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	var (
		body    io.Reader
		getBody func() (io.ReadCloser, error)
		size    int64
	)
	switch b := c.Body.(type) {
	case nil:
	case *Stream:
		rc, err := b.Open()
		if err != nil {
			return nil, fail("open request body: "+err.Error(), err)
		}
		body, getBody, size = rc, b.Open, b.Size
	default:
		payload, err := encodeBody(b)
		if err != nil {
			return nil, fail("encode request: "+err.Error(), err)
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, c.Method.Verb, u, body)
	if err != nil {
		if rc, ok := body.(io.Closer); ok {
			_ = rc.Close()
		}
		return nil, fail(err.Error(), err)
	}
	if getBody != nil {
		// net/http only derives GetBody and ContentLength for in-memory
		// readers; a stream sets both so a replay re-opens the source and the
		// upload is not sent chunked.
		req.GetBody, req.ContentLength = getBody, size
	}
	if c.Body != nil {
		ct := c.ContentType
		if ct == "" {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	return req, nil
}

// encodeBody turns a non-stream Body into its wire bytes.
func encodeBody(b any) ([]byte, error) {
	switch v := b.(type) {
	case []byte:
		return v, nil
	case json.RawMessage:
		return v, nil
	default:
		return json.Marshal(v)
	}
}

// bodyReadError tags a 2xx whose body could not be read to the end (a reset,
// a Client.Timeout firing mid-body). The request reached the server and was
// answered, so the fault is the network's, not the API's: Error.ExitCode maps
// it to the transport bucket (50), which a CI wrapper retries. A body that was
// read whole but does not decode is NOT tagged and keeps its own code.
type bodyReadError struct{ err error }

func (e *bodyReadError) Error() string { return e.err.Error() }
func (e *bodyReadError) Unwrap() error { return e.err }

// isBodyRead reports whether err carries a bodyReadError.
func isBodyRead(err error) bool {
	var b *bodyReadError
	return errors.As(err, &b)
}
