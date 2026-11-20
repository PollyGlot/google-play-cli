package api

import "fmt"

type Pager struct {
	Op, Target string
	Resource   Resource
	What       string
	Limit      int
	MaxPages   int
}

func Paginate[T any](p Pager, fetch func(token string, have int) (items []T, next string, err error)) (items []T, truncated bool, err error) {
	seen := map[string]struct{}{}
	token := ""
	for pages := 1; ; pages++ {
		page, next, err := fetch(token, len(items))
		if err != nil {
			return nil, false, err
		}
		items = append(items, page...)
		if p.Limit > 0 && len(items) >= p.Limit {
			truncated = next != "" || len(items) > p.Limit
			return items[:p.Limit], truncated, nil
		}
		if next == "" {
			return items, false, nil
		}
		if _, dup := seen[next]; dup {
			return nil, false, &Error{Operation: p.Op, Package: p.Target, Resource: p.Resource, Message: "pagination token loop detected in " + p.What + " (server repeated a nextPageToken)"}
		}
		if p.MaxPages > 0 && pages >= p.MaxPages {
			return nil, false, &Error{Operation: p.Op, Package: p.Target, Resource: p.Resource, Message: fmt.Sprintf("%s still had pages after %d requests: refusing a partial list", p.What, p.MaxPages)}
		}
		seen[next] = struct{}{}
		token = next
	}
}
