package apiregistry

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

var replaySafePOSTs = map[string]bool{
	"androidpublisher.edits.validate":                                 true,
	"androidpublisher.monetization.convertRegionPrices":               true,
	"playdeveloperreporting.vitals.anonrssandswapmemoryusage.query":   true,
	"playdeveloperreporting.vitals.anrrate.query":                     true,
	"playdeveloperreporting.vitals.bitmapmemoryusage.query":           true,
	"playdeveloperreporting.vitals.crashrate.query":                   true,
	"playdeveloperreporting.vitals.errors.counts.query":               true,
	"playdeveloperreporting.vitals.excessivewakeuprate.query":         true,
	"playdeveloperreporting.vitals.lmkrate.query":                     true,
	"playdeveloperreporting.vitals.slowrenderingrate.query":           true,
	"playdeveloperreporting.vitals.slowstartrate.query":               true,
	"playdeveloperreporting.vitals.stuckbackgroundwakelockrate.query": true,

	"androidpublisher.edits.insert": true,
}

func idempotentVerb(verb string) bool {
	switch strings.ToUpper(verb) {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return true
	default:
		return false
	}
}

func idempotent(id, verb string) bool {
	return idempotentVerb(verb) || replaySafePOSTs[id]
}

func paginated(idx schemaindex.Index, m schemaindex.Method) bool {
	s, ok := idx.Schemas[m.Response]
	if !ok {
		return false
	}
	_, next := s.Properties["nextPageToken"]
	_, legacy := s.Properties["tokenPagination"]
	return next || legacy
}

func IdempotentRequest(req *http.Request) bool {
	if m, ok := matchRequest(req); ok {
		return m.Idempotent
	}
	return idempotentVerb(req.Method)
}

func matchRequest(req *http.Request) (Method, bool) {
	if req == nil || req.URL == nil {
		return Method{}, false
	}
	u := req.URL.Scheme + "://" + req.URL.Host + req.URL.EscapedPath()
	for _, p := range matchers() {
		if p.verb == req.Method && p.re.MatchString(u) {
			return p.method, true
		}
	}
	return Method{}, false
}

type matcher struct {
	verb     string
	re       *regexp.Regexp
	literals int
	method   Method
}

var placeholder = regexp.MustCompile(`\{[^}]+\}`)

var matchers = sync.OnceValue(func() []matcher {
	var out []matcher
	for _, e := range entries {
		m, err := Resolve(e.MethodID)
		if err != nil {
			continue
		}
		for _, tmpl := range []string{m.URLTemplate, m.UploadTemplate} {
			if tmpl == "" {
				continue
			}
			lits := placeholder.Split(tmpl, -1)
			quoted := make([]string, len(lits))
			n := 0
			for i, l := range lits {
				quoted[i] = regexp.QuoteMeta(l)
				n += len(l)
			}
			out = append(out, matcher{
				verb:     m.Verb,
				re:       regexp.MustCompile("^" + strings.Join(quoted, "[^/]+") + "$"),
				literals: n,
				method:   m,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].literals > out[j].literals })
	return out
})
