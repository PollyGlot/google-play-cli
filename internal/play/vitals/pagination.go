package vitals

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/output"
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
// Parse* projectors and the JSON pass-through both consume. No items is an
// empty array, never null.
func rebuildEnvelope(itemsKey string, items []json.RawMessage) (json.RawMessage, error) {
	if items == nil {
		items = []json.RawMessage{}
	}
	return output.Marshal(map[string][]json.RawMessage{itemsKey: items})
}

// paginateGET fetches every page of a GET list/search endpoint through
// api.Paginate, following nextPageToken until exhausted or until `limit` items
// have accumulated (0 = all), and returns a rebuilt {itemsKey: [...]} envelope.
// base carries the stable query params (filter, interval, orderBy); pageSize
// bounds each request.
//
// The second return value is the TRUNCATION signal (api.Paginate's): true when
// the limit hid items, i.e. the envelope is a prefix of the truth rather than
// the whole of it. It is a return value, not a stderr write, because this
// layer is HTTP-only: the command layer owns the user-facing note (PRD #446).
// Hitting the limit exactly on the last page (no token left) is exhaustive, so
// it reports false.
//
// `next != ""` is taken at its word: the Reporting API documents the token as
// omitted once there are no subsequent pages, so a present token IS the server
// saying more exists. If a server ever handed back a token for an empty final
// page, the cost is one over-cautious `warning:` on stderr, never wrong data on
// stdout: the opposite mistake (staying silent while results were hidden) is
// the one PRD #446 exists to prevent.
func paginateGET(ctx context.Context, hc *http.Client, m apiregistry.Method, op, pkg string, base url.Values, itemsKey string, pageSize, limit int) (json.RawMessage, bool, error) {
	items, truncated, err := api.Paginate(api.Pager{Op: op, Target: pkg, What: itemsKey + " search", Limit: limit},
		func(token string, have int) ([]json.RawMessage, string, error) {
			q := url.Values{}
			for k, vs := range base {
				q[k] = append([]string(nil), vs...)
			}
			if ps := pageStep(pageSize, limit, have); ps > 0 {
				q.Set("pageSize", strconv.Itoa(ps))
			}
			if token != "" {
				q.Set("pageToken", token)
			}
			// Discovery names the path parameter after the collection, hence
			// appsId for what gplay calls the package.
			raw, err := api.Do(ctx, hc, api.Call{Method: m, Op: op, Target: pkg, Params: map[string]string{"appsId": pkg}, Query: q})
			if err != nil {
				return nil, "", err
			}
			page, next, derr := decodePage(raw, itemsKey)
			if derr != nil {
				return nil, "", &api.Error{Operation: op, Package: pkg, Message: "decode response: " + derr.Error(), Cause: derr}
			}
			return page, next, nil
		})
	if err != nil {
		return nil, false, err
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
