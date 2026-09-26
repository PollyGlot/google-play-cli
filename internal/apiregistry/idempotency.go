package apiregistry

// Idempotency and pagination: the two per-method facts the transport and the
// request executor need, declared here next to the verb and URL they are
// derived from (#575).
//
// Why the registry owns them. `--retry` replays a request after a transport
// error or a 5xx, and the first attempt may already have been applied
// server-side. That is harmless for a read or a full replace (GET, PUT), and a
// disaster for a create or an append (edits.images.upload APPENDS to a
// gallery, orders.refund moves money). The transport used to guess from a path
// suffix (`:commit`); it now asks this package, which answers from Google's own
// verb plus one explicit allowlist, so a new registered POST is non-idempotent
// until someone argues otherwise in a reviewed diff.

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// replaySafePOSTs lists the POST methods a replay cannot hurt. Everything else
// sent with POST is a create, an append or a one-shot action, and is never
// replayed once it may have reached the server. Keep each entry justified: an
// entry here is a promise that `--retry` may send the call twice.
var replaySafePOSTs = map[string]bool{
	// Read-shaped: POST only because the query rides a body.
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

	// A write whose duplicate has no effect: an Edit is a server-side draft
	// that changes nothing until committed, so a second one opened by a replay
	// is simply never used and expires. Leaving it out would make every
	// Edit-scoped command lose --retry on its very first call.
	"androidpublisher.edits.insert": true,
}

// idempotentVerb is the verb-level default: GET, HEAD, PUT and DELETE are
// idempotent by HTTP semantics, and Google's PATCH (an update of the fields
// named by the mask, allowMissing included) converges to the same state when
// sent twice. POST is not, and any verb gplay does not send is treated as not
// idempotent rather than guessed.
func idempotentVerb(verb string) bool {
	switch strings.ToUpper(verb) {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return true
	default:
		return false
	}
}

// idempotent is the declared bit of one registered method.
func idempotent(id, verb string) bool {
	return idempotentVerb(verb) || replaySafePOSTs[id]
}

// paginated reports whether a method's response is one page of a longer
// listing: its response schema carries a continuation token, `nextPageToken`
// for every current surface or `tokenPagination` for the legacy inappproducts
// list. Read from the schema rather than from a `pageToken` query parameter
// because the vitals `*.query` methods take their token in the POST body.
func paginated(idx schemaindex.Index, m schemaindex.Method) bool {
	s, ok := idx.Schemas[m.Response]
	if !ok {
		return false
	}
	_, next := s.Properties["nextPageToken"]
	_, legacy := s.Properties["tokenPagination"]
	return next || legacy
}

// IdempotentRequest reports whether req may be sent again after an attempt
// that may have reached the server. The answer is the declared Idempotent bit
// of the registered method req addresses (matched on verb and URL, media
// endpoint included); a request that matches no registered method falls back
// to its verb, so an unknown POST is never replayed.
func IdempotentRequest(req *http.Request) bool {
	if m, ok := matchRequest(req); ok {
		return m.Idempotent
	}
	return idempotentVerb(req.Method)
}

// matchRequest finds the registered method req addresses. The URLs gplay sends
// are built from these very templates (Method.URL / UploadURL), so reversing
// them is exact rather than heuristic; a test round-trips every entry.
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

// matcher is one compiled URL template.
type matcher struct {
	verb     string
	re       *regexp.Regexp
	literals int // literal characters in the template, the specificity key
	method   Method
}

// placeholder matches one `{param}` of a template.
var placeholder = regexp.MustCompile(`\{[^}]+\}`)

// matchers compiles every registered template once. A `{param}` matches one
// non-empty path segment fragment (no slash), exactly what expand's PathEscape
// can produce. Templates are ordered most literal first, so where two
// same-verb templates could both match (a `{id}` segment versus `{id}:verb`),
// the one that pins more of the URL wins.
var matchers = sync.OnceValue(func() []matcher {
	var out []matcher
	for _, e := range entries {
		m, err := Resolve(e.MethodID)
		if err != nil {
			// A registry test already fails on an unresolvable entry; the
			// classifier just skips it and falls back to the verb.
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
