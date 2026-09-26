package api

import "fmt"

// Pager describes one auto-paginated listing for Paginate: how to tag the
// error a misbehaving server raises, and where the walk stops.
type Pager struct {
	// Op and Target tag the *Error a repeated token raises, like Call's.
	Op, Target string
	// What names the listing in that error ("users.list").
	What string
	// Limit stops the walk once this many items have accumulated; 0 reads
	// every page.
	Limit int
	// MaxPages bounds the requests sent; 0 leaves the repeated-token guard as
	// the only bound. Reaching it while the server still hands back a token is
	// an error, never a silent stop: a partial list that looks whole is the
	// failure this seam exists to prevent.
	MaxPages int
}

// Paginate follows a continuation token to the end of a listing (DESIGN §9:
// auto-paginated reads never truncate silently). fetch sends one page request
// for token ("" for the first page) and returns that page's items and the
// next token ("" on the last page); have is how many items are already held,
// for a caller that sizes the last page to the limit.
//
// truncated reports that Pager.Limit hid items: the server still had pages, or
// the last page overshot the limit and was cut. The caller turns it into the
// stderr warning (rc.WarnTruncated); this layer only returns it.
//
// items is nil when the listing is empty, as a hand-written append loop leaves
// it; a caller that renders an empty array normalises it.
//
// A token the server already sent fails the walk with an *Error instead of
// requesting the same page until the context expires.
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
			// Both signals are read before the cut, which destroys the second:
			// a token left means more exists, and a page past the limit means
			// rows we hold are being dropped.
			truncated = next != "" || len(items) > p.Limit
			return items[:p.Limit], truncated, nil
		}
		if next == "" {
			return items, false, nil
		}
		if _, dup := seen[next]; dup {
			return nil, false, &Error{Operation: p.Op, Package: p.Target, Message: "pagination token loop detected in " + p.What + " (server repeated a nextPageToken)"}
		}
		if p.MaxPages > 0 && pages >= p.MaxPages {
			return nil, false, &Error{Operation: p.Op, Package: p.Target, Message: fmt.Sprintf("%s still had pages after %d requests: refusing a partial list", p.What, p.MaxPages)}
		}
		seen[next] = struct{}{}
		token = next
	}
}
