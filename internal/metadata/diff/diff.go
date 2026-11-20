package diff

import (
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
)

type Op string

const (
	OpCreate          Op = "create"
	OpUpdate          Op = "update"
	OpClear           Op = "clear"
	OpUnchanged       Op = "unchanged"
	OpUntouchedLocale Op = "untouchedLocale"
	OpDelete          Op = "delete"
)

const (
	untouchedReason = "on Play, absent locally"
	prunedReason    = "pruned: on Play, absent locally"
)

type Change struct {
	Locale     string `json:"locale"`
	Field      string `json:"field,omitempty"`
	Op         Op     `json:"op"`
	LiveChars  *int   `json:"liveChars,omitempty"`
	LocalChars *int   `json:"localChars,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type Summary struct {
	Create           int `json:"create"`
	Update           int `json:"update"`
	Clear            int `json:"clear"`
	Unchanged        int `json:"unchanged"`
	UntouchedLocales int `json:"untouchedLocales"`
	Delete           int `json:"delete"`
}

type Result struct {
	Package string   `json:"package"`
	Changes []Change `json:"changes"`
	Summary Summary  `json:"summary"`
}

func (r Result) HasChanges() bool {
	s := r.Summary
	return s.Create+s.Update+s.Clear+s.Delete > 0
}

func intPtr(n int) *int { return &n }

func Compute(pkg string, local, online listing.Tree, prune bool) Result {
	res := Result{Package: pkg, Changes: []Change{}}

	for _, loc := range local.Locales() {
		ll := local[loc]
		ol, onlineHasLocale := online[loc]
		for _, f := range listing.Fields() {
			localVal, managed := ll.Get(f)
			if !managed {
				continue
			}
			spec, ok := listing.SpecOf(f)
			if !ok {
				continue
			}

			var onlineVal string
			var onlineHasField bool
			if onlineHasLocale {
				onlineVal, onlineHasField = ol.Get(f)
			}
			localChars := listing.CharCount(localVal)
			liveChars := listing.CharCount(onlineVal)

			switch {
			case localVal != "":
				switch {
				case !onlineHasField:
					res.Summary.Create++
					res.Changes = append(res.Changes, Change{
						Locale: loc, Field: spec.Key, Op: OpCreate,
						LiveChars: intPtr(0), LocalChars: intPtr(localChars),
					})
				case onlineVal != localVal:
					res.Summary.Update++
					res.Changes = append(res.Changes, Change{
						Locale: loc, Field: spec.Key, Op: OpUpdate,
						LiveChars: intPtr(liveChars), LocalChars: intPtr(localChars),
					})
				default:
					res.Summary.Unchanged++
				}
			default:
				if onlineHasField {
					res.Summary.Clear++
					res.Changes = append(res.Changes, Change{
						Locale: loc, Field: spec.Key, Op: OpClear,
						LiveChars: intPtr(liveChars), LocalChars: intPtr(0),
					})
				} else {
					res.Summary.Unchanged++
				}
			}
		}
	}

	onlineOnly := make([]string, 0)
	for _, loc := range online.Locales() {
		if _, onDisk := local[loc]; !onDisk {
			onlineOnly = append(onlineOnly, loc)
		}
	}
	sort.Strings(onlineOnly)
	for _, loc := range onlineOnly {
		if prune {
			res.Summary.Delete++
			res.Changes = append(res.Changes, Change{
				Locale: loc, Op: OpDelete, Reason: prunedReason,
			})
		} else {
			res.Summary.UntouchedLocales++
			res.Changes = append(res.Changes, Change{
				Locale: loc, Op: OpUntouchedLocale, Reason: untouchedReason,
			})
		}
	}

	return res
}
