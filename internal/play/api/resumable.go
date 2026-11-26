package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/transport"
)

const ResumableChunkSize = 8 * 1024 * 1024

const maxResumeStalls = 8

type resumePacing struct {
	chunkTimeout time.Duration
	backoff      func(attempt int) time.Duration
	sleep        func(ctx context.Context, d time.Duration) bool
}

func defaultResumePacing() resumePacing {
	return resumePacing{chunkTimeout: 5 * time.Minute, backoff: transport.Backoff, sleep: transport.Sleep}
}

type LocalIOError struct {
	Op    string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: read upload source: %v", e.Op, e.Cause)
}

func (e *LocalIOError) Unwrap() error { return e.Cause }

func (e *LocalIOError) ExitCode() int { return 20 }

func ResumableUpload(
	ctx context.Context,
	hc *http.Client,
	op, pkg, initiateURL, contentType string,
	r io.ReaderAt,
	size int64,
) (body []byte, status int, err error) {
	return ResumableUploadWithInitiateBody(ctx, hc, op, PackageResource(pkg), initiateURL, contentType, r, size, nil, "", nil)
}

func ResumableUploadWithInitiateBody(
	ctx context.Context,
	hc *http.Client,
	op string, target Resource, initiateURL, contentType string,
	r io.ReaderAt,
	size int64,
	initiateBody []byte,
	initiateContentType string,
	query url.Values,
) (body []byte, status int, err error) {
	return resumableUpload(ctx, hc, op, target, initiateURL, contentType, r, size, initiateBody, initiateContentType, query, defaultResumePacing())
}

func resumableUpload(
	ctx context.Context,
	hc *http.Client,
	op string, target Resource, initiateURL, contentType string,
	r io.ReaderAt,
	size int64,
	initiateBody []byte,
	initiateContentType string,
	query url.Values,
	pace resumePacing,
) (body []byte, status int, err error) {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("uploadType", "resumable")
	sessionURI, err := resumableInitiate(ctx, hc, op, target, initiateURL+"?"+q.Encode(), contentType, size, initiateBody, initiateContentType)
	if err != nil {
		return nil, 0, err
	}

	noRetryCtx := transport.WithoutRetry(ctx)

	offset := int64(0)
	stalls := 0
	backOff := func() bool { return pace.sleep(ctx, pace.backoff(stalls)) }
	for {
		var stalled *Error
		if offset == size && size > 0 {
			stalled = &Error{Operation: op, Resource: target, StatusCode: 308, Message: "resumable upload stalled: server acknowledged every byte but did not finalize after " + strconv.Itoa(maxResumeStalls) + " probes"}
			if stalls > 0 && !backOff() {
				return nil, 0, &Error{Operation: op, Resource: target, Message: ctx.Err().Error(), Cause: ctx.Err()}
			}
		} else {
			resp, putErr := resumablePutChunk(noRetryCtx, hc, sessionURI, contentType, r, offset, size, op, pace.chunkTimeout)

			switch {
			case putErr != nil:
				var localErr *LocalIOError
				if errors.As(putErr, &localErr) {
					return nil, 0, putErr
				}
				if ctx.Err() != nil {
					return nil, 0, putErr
				}
				stalled = &Error{Operation: op, Resource: target, Message: "resumable upload stalled: server accepted no further bytes after " + strconv.Itoa(maxResumeStalls) + " resume attempts", Cause: putErr}

			case resp.StatusCode == 308:
				newOffset := committedOffset(resp, offset)
				drain(resp)
				if verr := validOffset(newOffset, size, op, target); verr != nil {
					return nil, 0, verr
				}
				if newOffset <= offset {
					if stalls++; stalls >= maxResumeStalls {
						return nil, 0, &Error{Operation: op, Resource: target, StatusCode: 308, Message: "resumable upload stalled: server acknowledged no progress after " + strconv.Itoa(maxResumeStalls) + " chunks"}
					}
					if !backOff() {
						return nil, 0, &Error{Operation: op, Resource: target, Message: ctx.Err().Error(), Cause: ctx.Err()}
					}
				} else {
					stalls = 0
				}
				offset = newOffset
				continue

			case resp.StatusCode >= 200 && resp.StatusCode < 300:
				out, rerr := io.ReadAll(io.LimitReader(resp.Body, MaxAPISuccessBodyRead))
				drain(resp)
				if rerr == nil {
					return out, resp.StatusCode, nil
				}
				stalled = &Error{Operation: op, Resource: target, StatusCode: resp.StatusCode, Message: "read response body: " + rerr.Error(), Cause: &bodyReadError{err: rerr}}
				if ctx.Err() != nil {
					return nil, 0, stalled
				}

			case resp.StatusCode >= 500:
				drain(resp)
				stalled = &Error{Operation: op, Resource: target, StatusCode: resp.StatusCode, Message: "resumable upload stalled: upstream 5xx and no progress after " + strconv.Itoa(maxResumeStalls) + " resume attempts"}

			default:
				return nil, 0, errorFromResponse(resp, op, target)
			}

			if !backOff() {
				return nil, 0, &Error{Operation: op, Resource: target, Message: ctx.Err().Error(), Cause: ctx.Err()}
			}
		}
		newOffset, done, doneBody, doneStatus, perr := resumableProbe(noRetryCtx, hc, sessionURI, size, op, target)
		if perr != nil {
			if !probeErrIsTransient(perr) || ctx.Err() != nil {
				return nil, 0, perr
			}
			if stalls++; stalls >= maxResumeStalls {
				return nil, 0, perr
			}
			continue
		}
		if done {
			return doneBody, doneStatus, nil
		}
		if verr := validOffset(newOffset, size, op, target); verr != nil {
			return nil, 0, verr
		}
		if newOffset <= offset {
			if stalls++; stalls >= maxResumeStalls {
				return nil, 0, stalled
			}
		} else {
			stalls = 0
		}
		offset = newOffset
	}
}

func resumableInitiate(ctx context.Context, hc *http.Client, op string, target Resource, initiateURL, contentType string, size int64, initiateBody []byte, initiateContentType string) (string, error) {
	var reqBody io.Reader = http.NoBody
	if initiateBody != nil {
		reqBody = bytes.NewReader(initiateBody)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, initiateURL, reqBody)
	if err != nil {
		return "", &Error{Operation: op, Resource: target, Message: err.Error(), Cause: err}
	}
	req.ContentLength = int64(len(initiateBody))
	if initiateBody != nil && initiateContentType != "" {
		req.Header.Set("Content-Type", initiateContentType)
	}
	req.Header.Set("X-Upload-Content-Type", contentType)
	req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(size, 10))
	resp, err := hc.Do(req)
	if err != nil {
		return "", &Error{Operation: op, Resource: target, Message: err.Error(), Cause: err}
	}
	defer drain(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errorFromResponse(resp, op, target)
	}
	uri := resp.Header.Get("Location")
	if uri == "" {
		return "", &Error{Operation: op, Resource: target, StatusCode: resp.StatusCode, Message: "resumable initiate returned no session URI (missing Location header)"}
	}
	return uri, nil
}

func resumablePutChunk(ctx context.Context, hc *http.Client, sessionURI, contentType string, r io.ReaderAt, offset, size int64, op string, timeout time.Duration) (*http.Response, error) {
	ctx, release := context.WithTimeout(ctx, timeout)
	resp, err := putChunk(ctx, hc, sessionURI, contentType, r, offset, size, op)
	if err != nil {
		release()
		return nil, err
	}
	resp.Body = releaseOnClose{ReadCloser: resp.Body, release: release}
	return resp, nil
}

type releaseOnClose struct {
	io.ReadCloser
	release context.CancelFunc
}

func (b releaseOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.release()
	return err
}

func putChunk(ctx context.Context, hc *http.Client, sessionURI, contentType string, r io.ReaderAt, offset, size int64, op string) (*http.Response, error) {
	end := offset + ResumableChunkSize
	if end > size {
		end = size
	}
	chunkLen := end - offset
	buf := make([]byte, chunkLen)
	if chunkLen > 0 {
		n, rerr := r.ReadAt(buf, offset)
		if n != len(buf) {
			if rerr == nil {
				rerr = io.ErrUnexpectedEOF
			}
			return nil, &LocalIOError{Op: op, Cause: rerr}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURI, bytes.NewReader(buf))
	if err != nil {
		return nil, &Error{Operation: op, Message: err.Error(), Cause: err}
	}
	req.ContentLength = chunkLen
	req.Header.Set("Content-Type", contentType)
	if size == 0 {
		req.Header.Set("Content-Range", "bytes */0")
	} else {
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, end-1, size))
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &Error{Operation: op, Message: err.Error(), Cause: err}
	}
	return resp, nil
}

func resumableProbe(ctx context.Context, hc *http.Client, sessionURI string, size int64, op string, target Resource) (offset int64, done bool, body []byte, status int, err error) {
	req, rerr := http.NewRequestWithContext(ctx, http.MethodPut, sessionURI, http.NoBody)
	if rerr != nil {
		return 0, false, nil, 0, &Error{Operation: op, Resource: target, Message: rerr.Error(), Cause: rerr}
	}
	req.ContentLength = 0
	req.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
	resp, derr := hc.Do(req)
	if derr != nil {
		return 0, false, nil, 0, &Error{Operation: op, Resource: target, Message: derr.Error(), Cause: derr}
	}
	switch {
	case resp.StatusCode == 308:
		off := committedOffset(resp, 0)
		drain(resp)
		return off, false, nil, 0, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		out, _ := io.ReadAll(io.LimitReader(resp.Body, MaxAPISuccessBodyRead))
		drain(resp)
		return 0, true, out, resp.StatusCode, nil
	default:
		return 0, false, nil, 0, errorFromResponse(resp, op, target)
	}
}

func committedOffset(resp *http.Response, fallback int64) int64 {
	rng := resp.Header.Get("Range")
	if rng == "" {
		return 0
	}
	i := strings.LastIndex(rng, "-")
	if i < 0 {
		return fallback
	}
	last, err := strconv.ParseInt(strings.TrimSpace(rng[i+1:]), 10, 64)
	if err != nil {
		return fallback
	}
	return last + 1
}

func validOffset(offset, size int64, op string, target Resource) *Error {
	if offset < 0 || offset > size {
		return &Error{Operation: op, Resource: target, Message: fmt.Sprintf("resumable upload: server acknowledged offset %d outside artifact size %d", offset, size)}
	}
	return nil
}

func probeErrIsTransient(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && (apiErr.StatusCode == 0 || apiErr.StatusCode >= 500)
}

func errorFromResponse(resp *http.Response, op string, target Resource) *Error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, MaxAPIErrorBodyRead))
	drain(resp)
	msg, reasons := ParseErrorEnvelope(b, resp.StatusCode)
	return &Error{
		Operation:  op,
		Resource:   target,
		StatusCode: resp.StatusCode,
		Message:    msg,
		Reasons:    reasons,
	}
}

func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
