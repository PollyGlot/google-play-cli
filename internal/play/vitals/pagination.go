package vitals

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// The Reporting API caps every list/query response with a small default page
// (e.g. 10 anomalies, 50 error issues) and returns a `nextPageToken`. A single
// page therefore silently under-reports. These helpers follow the token to
// completion (bounded by an optional total `limit`), then rebuild the response
// envelope from the verbatim item objects: items are preserved byte-for-byte
// so `--output json` stays an ADR-0003 pass-through even though several upstream
// pages were merged (the same stance internal/play/reviews takes after
// pagination).

// decodePage extracts the verbatim item objects under itemsKey and the
// continuation token from one page body. A zero-length body (a 204, or a proxy
// returning an empty 200) is tolerated as an empty, token-less page rather than
// surfacing as a JSON decode error.
func decodePage(raw []byte, itemsKey string) (items []json.RawMessage, next string, err error) {
	if len(raw) == 0 {
		return nil, "", nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, "", err
	}
	if v, ok := m[itemsKey]; ok && len(v) > 0 {
		if err := json.Unmarshal(v, &items); err != nil {
			return nil, "", err
		}
	}
	if v, ok := m["nextPageToken"]; ok {
		_ = json.Unmarshal(v, &next)
	}
	return items, next, nil
}

// rebuildEnvelope re-serialises accumulated items under itemsKey, the shape the
// Parse* projectors and the JSON pass-through both consume.
func rebuildEnvelope(itemsKey string, items []json.RawMessage) (json.RawMessage, error) {
	return json.Marshal(map[string][]json.RawMessage{itemsKey: items})
}

// tokenLoopError is the api.Error a non-progressing pagination token raises:
// the same defence internal/play/reviews uses, so a server that repeats a token
// fails loudly instead of looping until the context is cancelled.
func tokenLoopError(op, pkg, what string) error {
	return &api.Error{Operation: op, Package: pkg, Message: "pagination token loop detected in " + what + " (server repeated a nextPageToken)"}
}

// paginateGET fetches every page of a GET list/search endpoint, following
// nextPageToken until exhausted or until `limit` items have accumulated (0 =
// all), and returns a rebuilt {itemsKey: [...]} envelope. base carries the
// stable query params (filter, interval, orderBy); pageSize bounds each request.
//
// The second return value is the TRUNCATION signal: true when the loop stopped
// on `limit` while the server still handed back a nextPageToken, i.e. the
// envelope is a prefix of the truth rather than the whole of it. It is a return
// value, not a stderr write, because this layer is HTTP-only: the command layer
// owns the user-facing note (PRD #446). Hitting the limit exactly on the last
// page (no token left) is exhaustive, so it reports false.
func paginateGET(ctx context.Context, hc *http.Client, op, pkg, baseURL string, base url.Values, itemsKey string, pageSize, limit int) (json.RawMessage, bool, error) {
	items := make([]json.RawMessage, 0)
	seen := map[string]struct{}{}
	token := ""
	truncated := false
	for {
		q := url.Values{}
		for k, vs := range base {
			q[k] = append([]string(nil), vs...)
		}
		if ps := pageStep(pageSize, limit, len(items)); ps > 0 {
			q.Set("pageSize", strconv.Itoa(ps))
		}
		if token != "" {
			q.Set("pageToken", token)
		}
		u := baseURL
		if enc := q.Encode(); enc != "" {
			u += "?" + enc
		}
		raw, err := getRaw(ctx, hc, op, pkg, u)
		if err != nil {
			return nil, false, err
		}
		pageItems, next, derr := decodePage(raw, itemsKey)
		if derr != nil {
			return nil, false, &api.Error{Operation: op, Package: pkg, Message: "decode response: " + derr.Error(), Cause: derr}
		}
		items = append(items, pageItems...)
		if limit > 0 && len(items) >= limit {
			// Truncated means "the cap hid something that existed", and there
			// are two ways for that to happen. Either the server still has
			// pages (next != "") and the cap is why we stop asking, or this
			// page already OVERSHOT the cap (len > limit) and the slice below
			// drops rows we are holding: a single page of 15 under --limit 10
			// is a truncation even though no token remains. Both are computed
			// BEFORE the slice, because slicing destroys the second signal.
			//
			// `next != ""` is taken at its word: the Reporting API documents
			// the token as omitted once there are no subsequent pages, so a
			// present token IS the server saying more exists, and it is the
			// only signal available short of spending another request to find
			// out. If a server ever handed back a token for an empty final
			// page, the cost is one over-cautious `warning:` on stderr, never
			// wrong data on stdout: the opposite mistake (staying silent while
			// results were hidden) is the one PRD #446 exists to prevent.
			truncated = next != "" || len(items) > limit
			items = items[:limit]
			break
		}
		if next == "" {
			break
		}
		if _, dup := seen[next]; dup {
			return nil, false, tokenLoopError(op, pkg, itemsKey+" search")
		}
		seen[next] = struct{}{}
		token = next
	}
	envelope, err := rebuildEnvelope(itemsKey, items)
	if err != nil {
		return nil, false, err
	}
	return envelope, truncated, nil
}

// pageStep is the pageSize to request next: the resource default when no total
// limit is set, else just enough to reach the remaining cap (never more than
// the default page).
func pageStep(pageSize, limit, have int) int {
	if pageSize <= 0 {
		return 0
	}
	if limit <= 0 {
		return pageSize
	}
	remaining := limit - have
	if remaining < pageSize {
		return remaining
	}
	return pageSize
}

// getRaw issues a single GET and returns the verbatim 2xx body or an *api.Error.
func getRaw(ctx context.Context, hc *http.Client, op, pkg, u string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(errBody, resp.StatusCode)
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "read response: " + readErr.Error(), Cause: readErr}
	}
	return raw, nil
}
