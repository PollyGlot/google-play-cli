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

type Call struct {
	Method      apiregistry.Method
	Params      map[string]string
	Query       url.Values
	Body        any
	ContentType string
	Media       bool
	Op          string
	Target      string
	Resource    Resource
	URL         string
}

type Stream struct {
	Open func() (io.ReadCloser, error)
	Size int64
}

func Do(ctx context.Context, hc *http.Client, c Call) (json.RawMessage, error) {
	op := c.op()
	resp, err := send(ctx, hc, c, op)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxAPISuccessBodyRead+1))
	if err != nil {
		return nil, &Error{
			Operation: op, Package: c.Target, Resource: c.Resource, StatusCode: resp.StatusCode,
			Message: "read response body: " + err.Error(),
			Cause:   &bodyReadError{err: err},
		}
	}
	if len(raw) > MaxAPISuccessBodyRead {
		return nil, &Error{
			Operation: op, Package: c.Target, Resource: c.Resource, StatusCode: resp.StatusCode,
			Message: fmt.Sprintf("response body exceeds the %d-byte limit (%d MiB): refusing to truncate it", MaxAPISuccessBodyRead, MaxAPISuccessBodyRead>>20),
		}
	}
	return json.RawMessage(raw), nil
}

func Download(ctx context.Context, hc *http.Client, c Call, w io.Writer) (int64, error) {
	op := c.op()
	resp, err := send(ctx, hc, c, op)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	src := &readTracker{r: resp.Body}
	n, err := io.Copy(w, src)
	if err != nil {
		cause := err
		if src.err != nil {
			cause = &bodyReadError{err: err}
		}
		return n, &Error{
			Operation: op, Package: c.Target, Resource: c.Resource, StatusCode: resp.StatusCode,
			Message: "stream response body: " + err.Error(),
			Cause:   cause,
		}
	}
	return n, nil
}

func send(ctx context.Context, hc *http.Client, c Call, op string) (*http.Response, error) {
	req, err := c.request(ctx, op)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &Error{Operation: op, Package: c.Target, Resource: c.Resource, Message: err.Error(), Cause: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, MaxAPIErrorBodyRead))
		msg, reasons := ParseErrorEnvelope(b, resp.StatusCode)
		return nil, &Error{Operation: op, Package: c.Target, Resource: c.Resource, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	return resp, nil
}

type readTracker struct {
	r   io.Reader
	err error
}

func (t *readTracker) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		t.err = err
	}
	return n, err
}

func DoJSON(ctx context.Context, hc *http.Client, c Call, out any) (json.RawMessage, error) {
	raw, err := Do(ctx, hc, c)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, &Error{Operation: c.op(), Package: c.Target, Resource: c.Resource, Message: "decode response: " + err.Error(), Cause: err}
	}
	return raw, nil
}

func (c Call) op() string {
	if c.Op != "" {
		return c.Op
	}
	if i := strings.IndexByte(c.Method.ID, '.'); i >= 0 {
		return c.Method.ID[i+1:]
	}
	return c.Method.ID
}

func (c Call) request(ctx context.Context, op string) (*http.Request, error) {
	fail := func(msg string, err error) error {
		return &Error{Operation: op, Package: c.Target, Resource: c.Resource, Message: msg, Cause: err}
	}
	verb, u, q := http.MethodGet, c.URL, url.Values(nil)
	if u == "" {
		build := c.Method.URL
		if c.Media {
			build = c.Method.UploadURL
		}
		var err error
		if u, err = build(c.Params); err != nil {
			return nil, fail(err.Error(), err)
		}
		verb, q = c.Method.Verb, c.Query
		if c.Media {
			q = url.Values{}
			for k, v := range c.Query {
				q[k] = v
			}
			q.Set("uploadType", "media")
		}
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
	req, err := http.NewRequestWithContext(ctx, verb, u, body)
	if err != nil {
		if rc, ok := body.(io.Closer); ok {
			_ = rc.Close()
		}
		return nil, fail(err.Error(), err)
	}
	if getBody != nil {
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

type bodyReadError struct{ err error }

func (e *bodyReadError) Error() string { return e.err.Error() }
func (e *bodyReadError) Unwrap() error { return e.err }

func isBodyRead(err error) bool {
	var b *bodyReadError
	return errors.As(err, &b)
}
