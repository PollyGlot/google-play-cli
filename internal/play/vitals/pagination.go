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

func rebuildEnvelope(itemsKey string, items []json.RawMessage) (json.RawMessage, error) {
	if items == nil {
		items = []json.RawMessage{}
	}
	return output.Marshal(map[string][]json.RawMessage{itemsKey: items})
}

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
			raw, err := api.Do(ctx, hc, api.Call{Method: m, Op: op, Target: pkg, Params: map[string]string{"appsId": pkg}, Query: q})
			if err != nil {
				return nil, "", err
			}
			page, next, derr := decodePage(raw, itemsKey)
			if derr != nil {
				return nil, "", api.DecodeFailure(api.Error{Operation: op, Package: pkg, Message: "decode response: " + derr.Error(), Cause: derr})
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
