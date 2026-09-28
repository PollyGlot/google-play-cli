package api_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// pages serves n pages of two items each; the token of page i is "t<i>".
func pages(n int, tokens *[]string) func(string, int) ([]string, string, error) {
	return func(token string, _ int) ([]string, string, error) {
		*tokens = append(*tokens, token)
		i := 0
		if token != "" {
			i, _ = strconv.Atoi(strings.TrimPrefix(token, "t"))
		}
		next := ""
		if i+1 < n {
			next = "t" + strconv.Itoa(i+1)
		}
		return []string{strconv.Itoa(2 * i), strconv.Itoa(2*i + 1)}, next, nil
	}
}

func TestPaginate_followsEveryPage(t *testing.T) {
	var tokens []string
	items, truncated, err := api.Paginate(api.Pager{What: "x.list"}, pages(3, &tokens))
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	if !reflect.DeepEqual(items, []string{"0", "1", "2", "3", "4", "5"}) || !reflect.DeepEqual(tokens, []string{"", "t1", "t2"}) {
		t.Errorf("items=%v tokens=%v", items, tokens)
	}
}

func TestPaginate_limit(t *testing.T) {
	for _, c := range []struct {
		limit, n  int
		want      []string
		truncated bool
	}{
		{limit: 4, n: 3, want: []string{"0", "1", "2", "3"}, truncated: true}, // a token was left
		{limit: 3, n: 3, want: []string{"0", "1", "2"}, truncated: true},      // page overshot the limit
		{limit: 2, n: 1, want: []string{"0", "1"}, truncated: false},          // exact, last page
		{limit: 9, n: 2, want: []string{"0", "1", "2", "3"}, truncated: false},
	} {
		var tokens []string
		items, truncated, err := api.Paginate(api.Pager{Limit: c.limit}, pages(c.n, &tokens))
		if err != nil || truncated != c.truncated || !reflect.DeepEqual(items, c.want) {
			t.Errorf("limit %d over %d pages: items=%v truncated=%v err=%v, want %v truncated=%v", c.limit, c.n, items, truncated, err, c.want, c.truncated)
		}
	}
}

func TestPaginate_passesHowManyItemsAreHeld(t *testing.T) {
	var have []int
	_, _, _ = api.Paginate(api.Pager{}, func(token string, n int) ([]int, string, error) {
		have = append(have, n)
		if token == "" {
			return []int{1, 2, 3}, "t", nil
		}
		return []int{4}, "", nil
	})
	if !reflect.DeepEqual(have, []int{0, 3}) {
		t.Errorf("have = %v, want [0 3]", have)
	}
}

func TestPaginate_refusesARepeatedToken(t *testing.T) {
	calls := 0
	_, _, err := api.Paginate(api.Pager{Op: "users.list", Target: "dev", What: "users.list"}, func(string, int) ([]int, string, error) {
		calls++
		return []int{calls}, "same", nil
	})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Operation != "users.list" || ae.Package != "dev" ||
		ae.Message != "pagination token loop detected in users.list (server repeated a nextPageToken)" {
		t.Fatalf("err = %#v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (the repeat is caught on its first return)", calls)
	}
}

func TestPaginate_maxPagesFailsLoudly(t *testing.T) {
	var tokens []string
	_, _, err := api.Paginate(api.Pager{Op: "apps.search", What: "apps.search", MaxPages: 2}, pages(5, &tokens))
	if err == nil || !strings.Contains(err.Error(), "apps.search still had pages after 2 requests") || len(tokens) != 2 {
		t.Fatalf("err=%v after %d requests, want a loud stop after 2", err, len(tokens))
	}
	// The bound is not a failure when the listing ends within it.
	tokens = nil
	items, _, err := api.Paginate(api.Pager{MaxPages: 2}, pages(2, &tokens))
	if err != nil || len(items) != 4 {
		t.Errorf("items=%v err=%v, want 4 items", items, err)
	}
}

func TestPaginate_emptyListingIsNilAndFetchErrorsPassThrough(t *testing.T) {
	items, _, err := api.Paginate(api.Pager{}, func(string, int) ([]int, string, error) { return nil, "", nil })
	if err != nil || items != nil {
		t.Errorf("items=%#v err=%v, want nil, nil", items, err)
	}
	boom := errors.New("boom")
	if _, _, err := api.Paginate(api.Pager{}, func(string, int) ([]int, string, error) { return nil, "", boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the fetch error", err)
	}
}
