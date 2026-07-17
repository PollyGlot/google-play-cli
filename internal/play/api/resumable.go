package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/transport"
)

// ResumableChunkSize is the fixed size of each PUT in the resumable-upload
// protocol: 8 MiB, a whole multiple of the protocol's 256 KiB granularity
// requirement (Google rejects intermediate chunks that are not). It is
// deliberately not configurable (PRD #355): resumable is unconditional, with
// no size threshold and no flag.
const ResumableChunkSize = 8 * 1024 * 1024

// maxResumeStalls bounds how many consecutive transient failures the helper
// tolerates WITHOUT the server's committed offset advancing. The retry
// transport is disabled for these requests (WithoutRetry), so this counter is
// what stops an unbounded loop against a server that keeps 5xx-ing or dropping
// the connection while never accepting more bytes. A single failure that is
// followed by progress resets the counter, so a genuine resume is unaffected.
const maxResumeStalls = 8

// LocalIOError is returned when the upload source cannot be read locally
// (a mid-upload ReadAt failure on the artifact). It is distinct from *Error so
// exit.For maps it to client-side validation (exit 20 per docs/DESIGN.md §9)
// rather than a transport failure (exit 50): the bytes never left the machine.
type LocalIOError struct {
	Op    string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: read upload source: %v", e.Op, e.Cause)
}

func (e *LocalIOError) Unwrap() error { return e.Cause }

// ExitCode satisfies gplay's Coder contract: a local read failure is a
// client-side problem, not an upstream one.
func (e *LocalIOError) ExitCode() int { return 20 }

// ResumableUpload performs a Google resumable media upload and returns the
// final 2xx response body verbatim (for ADR-0003 JSON pass-through) plus its
// status code.
//
// The protocol has three moves:
//
//  1. Initiate — POST initiateURL (which the caller builds with
//     uploadType=resumable) carrying X-Upload-Content-Type / -Length and an
//     empty body. The server answers 200/201 with a session URI in Location.
//  2. Upload — PUT the content to the session URI in fixed ResumableChunkSize
//     chunks, each with a Content-Range: bytes {start}-{end}/{total} header.
//     An intermediate chunk is acknowledged with 308 (Resume Incomplete) whose
//     Range header names the last stored byte; the final chunk returns 2xx
//     with the resource body.
//  3. Resume — on a transient PUT failure (transport error or 5xx) the helper
//     PUTs Content-Range: bytes *\/{total} with an empty body to ask the server
//     which byte it last committed (another 308 + Range), then continues from
//     that offset. No bytes are ever re-sent past the acknowledged offset.
//
// r must provide random access to exactly size bytes. Chunk PUTs and the
// resume probe run under transport.WithoutRetry so the shared --retry
// middleware never double-sends them; the helper owns recovery. Upstream
// failures map to *Error (the DESIGN §9 exit taxonomy); a local read failure
// on r maps to *LocalIOError (exit 20).
func ResumableUpload(
	ctx context.Context,
	hc *http.Client,
	op, pkg, initiateURL, contentType string,
	r io.ReaderAt,
	size int64,
) (body []byte, status int, err error) {
	sessionURI, err := resumableInitiate(ctx, hc, op, pkg, initiateURL, contentType, size)
	if err != nil {
		return nil, 0, err
	}

	// noRetryCtx excludes every chunk PUT / resume probe from the retry
	// middleware: this helper is the retry mechanism for the transfer.
	noRetryCtx := transport.WithoutRetry(ctx)

	offset := int64(0)
	stalls := 0
	for {
		// A zero-length artifact still needs one PUT to finalize the session;
		// beyond that, offset == size means the server already has everything.
		resp, putErr := resumablePutChunk(noRetryCtx, hc, sessionURI, contentType, r, offset, size, op)
		if putErr != nil {
			// A local read failure is terminal (exit 20) — no point resuming.
			if _, ok := putErr.(*LocalIOError); ok {
				return nil, 0, putErr
			}
			// Transport failure: probe the committed offset and resume.
			newOffset, done, doneBody, doneStatus, perr := resumableProbe(noRetryCtx, hc, sessionURI, size, op, pkg)
			if perr != nil {
				return nil, 0, perr
			}
			if done {
				return doneBody, doneStatus, nil
			}
			if newOffset <= offset {
				if stalls++; stalls >= maxResumeStalls {
					return nil, 0, &Error{Operation: op, Package: pkg, Message: "resumable upload stalled: server accepted no further bytes after " + strconv.Itoa(maxResumeStalls) + " resume attempts", Cause: putErr}
				}
			} else {
				stalls = 0
			}
			offset = newOffset
			continue
		}

		switch {
		case resp.StatusCode == 308:
			// Resume Incomplete: advance to the server's acknowledged offset.
			newOffset := committedOffset(resp, offset)
			drain(resp)
			if newOffset <= offset {
				if stalls++; stalls >= maxResumeStalls {
					return nil, 0, &Error{Operation: op, Package: pkg, StatusCode: 308, Message: "resumable upload stalled: server acknowledged no progress after " + strconv.Itoa(maxResumeStalls) + " chunks"}
				}
			} else {
				stalls = 0
			}
			offset = newOffset
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			// Final chunk accepted: the body is the resource (pass-through).
			out, _ := io.ReadAll(io.LimitReader(resp.Body, MaxAPISuccessBodyRead))
			drain(resp)
			return out, resp.StatusCode, nil
		case resp.StatusCode >= 500:
			// Transient upstream error mid-upload: probe + resume.
			drain(resp)
			newOffset, done, doneBody, doneStatus, perr := resumableProbe(noRetryCtx, hc, sessionURI, size, op, pkg)
			if perr != nil {
				return nil, 0, perr
			}
			if done {
				return doneBody, doneStatus, nil
			}
			if newOffset <= offset {
				if stalls++; stalls >= maxResumeStalls {
					return nil, 0, &Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "resumable upload stalled: upstream 5xx and no progress after " + strconv.Itoa(maxResumeStalls) + " resume attempts"}
				}
			} else {
				stalls = 0
			}
			offset = newOffset
		default:
			// Terminal 4xx (bad artifact, auth, conflict, gone): surface it.
			return nil, 0, errorFromResponse(resp, op, pkg)
		}
	}
}

// resumableInitiate does the POST that opens a resumable session and returns
// the session URI (the Location header value).
func resumableInitiate(ctx context.Context, hc *http.Client, op, pkg, initiateURL, contentType string, size int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, initiateURL, http.NoBody)
	if err != nil {
		return "", &Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.ContentLength = 0
	req.Header.Set("X-Upload-Content-Type", contentType)
	req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(size, 10))
	resp, err := hc.Do(req)
	if err != nil {
		return "", &Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer drain(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errorFromResponse(resp, op, pkg)
	}
	uri := resp.Header.Get("Location")
	if uri == "" {
		return "", &Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "resumable initiate returned no session URI (missing Location header)"}
	}
	return uri, nil
}

// resumablePutChunk sends the [offset, min(offset+chunk, size)) slice of r to
// the session URI. It returns the raw response for the caller to classify. A
// read failure on r yields a *LocalIOError.
func resumablePutChunk(ctx context.Context, hc *http.Client, sessionURI, contentType string, r io.ReaderAt, offset, size int64, op string) (*http.Response, error) {
	end := offset + ResumableChunkSize
	if end > size {
		end = size
	}
	chunkLen := end - offset
	buf := make([]byte, chunkLen)
	if chunkLen > 0 {
		if _, rerr := r.ReadAt(buf, offset); rerr != nil && rerr != io.EOF {
			return nil, &LocalIOError{Op: op, Cause: rerr}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURI, bytes.NewReader(buf))
	if err != nil {
		return nil, &Error{Operation: op, Message: err.Error(), Cause: err}
	}
	req.ContentLength = chunkLen
	req.Header.Set("Content-Type", contentType)
	// Content-Range for a zero-length upload uses the bytes *\/0 form.
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

// resumableProbe asks the server for the number of bytes it has committed by
// PUTting an empty body with Content-Range: bytes *\/{size}. It returns the new
// offset, or done=true (with the resource body + status) if the server reports
// the upload already complete (a 2xx to the probe).
func resumableProbe(ctx context.Context, hc *http.Client, sessionURI string, size int64, op, pkg string) (offset int64, done bool, body []byte, status int, err error) {
	req, rerr := http.NewRequestWithContext(ctx, http.MethodPut, sessionURI, http.NoBody)
	if rerr != nil {
		return 0, false, nil, 0, &Error{Operation: op, Package: pkg, Message: rerr.Error(), Cause: rerr}
	}
	req.ContentLength = 0
	req.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
	resp, derr := hc.Do(req)
	if derr != nil {
		return 0, false, nil, 0, &Error{Operation: op, Package: pkg, Message: derr.Error(), Cause: derr}
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
		return 0, false, nil, 0, errorFromResponse(resp, op, pkg)
	}
}

// committedOffset reads a resumable 308 Range header ("bytes=0-{last}") and
// returns the next byte to send (last+1). A missing/garbled header means the
// server has stored nothing new, so fallback is returned unchanged.
func committedOffset(resp *http.Response, fallback int64) int64 {
	rng := resp.Header.Get("Range")
	if rng == "" {
		// No Range on a 308 means zero bytes committed so far.
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

// errorFromResponse builds an *Error from a non-success response, parsing the
// Google error envelope for the message and reasons.
func errorFromResponse(resp *http.Response, op, pkg string) *Error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, MaxAPIErrorBodyRead))
	drain(resp)
	msg, reasons := ParseErrorEnvelope(b, resp.StatusCode)
	return &Error{
		Operation:  op,
		Package:    pkg,
		StatusCode: resp.StatusCode,
		Message:    msg,
		Reasons:    reasons,
	}
}

// drain reads and closes resp.Body so the underlying connection can be reused.
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
