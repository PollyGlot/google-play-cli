package apiregistry

// Resolver: turning a registered method id into the request the CLI must send.
//
// Why it exists (#513, expand step). The registry above proves that every
// method gplay calls still exists upstream, but it cannot prove the converse:
// that every call the CLI makes is registered. Call sites hand-roll their URLs
// from local `op*` constants and string literals (ADR-0007 keeps us off the
// generated client), so a new call could ship with no registry line and no test
// would notice. Once call sites obtain their verb and URL from here, the
// registry becomes complete by construction: an unregistered method has no way
// to reach the wire.
//
// Shape of the API, and why it is this small. Migration batches follow, package
// by package, so the surface has to be obvious and impossible to misuse:
//
//   - Resolve(id) answers the two questions a call site asks, verb and URL
//     template, and refuses an id that is not registered AND present in the
//     embedded Schema index. Everything is derived from the index, so the
//     answer is Google's own Discovery data, never a second hand-kept table.
//   - Method.URL(params) fills the `{param}` placeholders. It is a method on
//     the resolved value rather than a free function so a call site cannot fill
//     one method's template while sending another method's verb.
//   - Method.UploadURL(params) is the media endpoint, a genuinely different
//     path (`/upload/...`, not a suffix of the normal one), so it gets its own
//     accessor instead of a boolean argument.
//
// Deliberately absent: query-string building and request execution. Those stay
// in internal/play/*, where the per-command semantics live; the resolver's only
// job is to stop paths and verbs from being written by hand.

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// Method is a resolved API method: what to send and where. Verb and the two
// templates come straight from the embedded Schema index, which is derived from
// the committed Discovery snapshots.
type Method struct {
	// ID is the native RPC method id, e.g. `androidpublisher.edits.tracks.get`.
	ID string
	// Verb is the HTTP verb, upper-case, as Discovery declares it.
	Verb string
	// URLTemplate is the absolute URL with `{param}` placeholders left in, e.g.
	// "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/{packageName}/edits/{editId}".
	URLTemplate string
	// UploadTemplate is the absolute media-upload URL template, empty for a
	// method that accepts no payload.
	UploadTemplate string
}

// URL fills URLTemplate's placeholders with params and returns the absolute
// URL. Values are path-escaped, so a package name or a locale carrying a slash
// or a space cannot forge extra path segments.
func (m Method) URL(params map[string]string) (string, error) {
	return expand(m.ID, m.URLTemplate, params)
}

// UploadURL is URL for the media-upload endpoint. It fails on a method that has
// no upload endpoint rather than silently returning the data-plane URL.
func (m Method) UploadURL(params map[string]string) (string, error) {
	if m.UploadTemplate == "" {
		return "", fmt.Errorf("api method %s: no media-upload endpoint", m.ID)
	}
	return expand(m.ID, m.UploadTemplate, params)
}

// Resolve returns the method registered under id. It fails on an id that is not
// in the registry (the call site must declare what it calls) and on one the
// embedded Schema index does not know (the API dropped it, or the index is
// stale: run `make schema-index-update`).
func Resolve(id string) (Method, error) {
	idx, err := loadIndex()
	if err != nil {
		return Method{}, err
	}
	if !registered()[id] {
		return Method{}, fmt.Errorf("api method %s: not in the registry, add it to internal/apiregistry", id)
	}
	m, ok := idx.Methods[id]
	if !ok {
		return Method{}, fmt.Errorf("api method %s: absent from the embedded Schema index", id)
	}
	svc, ok := idx.Services[serviceOf(id)]
	if !ok {
		return Method{}, fmt.Errorf("api method %s: no service entry for %q in the Schema index", id, serviceOf(id))
	}
	out := Method{
		ID:          id,
		Verb:        m.HTTPMethod,
		URLTemplate: svc.RootURL + svc.BasePath + m.Path,
	}
	if m.UploadPath != "" {
		out.UploadTemplate = svc.RootURL + m.UploadPath
	}
	return out, nil
}

// MustResolve is Resolve for package-level initialisation, where a failure is a
// programming error (an unregistered id or a stale index) that must not be
// deferred to the first user who runs the command.
func MustResolve(id string) Method {
	m, err := Resolve(id)
	if err != nil {
		panic(err)
	}
	return m
}

// serviceOf extracts the service discriminator, the leading segment of a method
// id, which is how Index.Services is keyed.
func serviceOf(id string) string {
	if i := strings.IndexByte(id, '.'); i > 0 {
		return id[:i]
	}
	return id
}

// expand substitutes every `{name}` placeholder of tmpl. A missing or empty
// value is an error naming the parameter: an empty package name would otherwise
// produce a plausible-looking URL that 404s far from its cause. An unused param
// is an error too, since it is almost always a typo in the key.
func expand(id, tmpl string, params map[string]string) (string, error) {
	used := make(map[string]bool, len(params))
	var b strings.Builder
	rest := tmpl
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			b.WriteString(rest)
			break
		}
		end := strings.IndexByte(rest[open:], '}')
		if end < 0 {
			return "", fmt.Errorf("api method %s: malformed URL template %q", id, tmpl)
		}
		end += open
		name := rest[open+1 : end]
		v, ok := params[name]
		if !ok || v == "" {
			return "", fmt.Errorf("api method %s: missing value for path parameter %q", id, name)
		}
		used[name] = true
		b.WriteString(rest[:open])
		b.WriteString(url.PathEscape(v))
		rest = rest[end+1:]
	}
	if len(used) != len(params) {
		extra := make([]string, 0, len(params)-len(used))
		for k := range params {
			if !used[k] {
				extra = append(extra, k)
			}
		}
		sort.Strings(extra)
		return "", fmt.Errorf("api method %s: unknown path parameter(s) %s", id, strings.Join(extra, ", "))
	}
	return b.String(), nil
}

// loadIndex parses the embedded Schema index once per process: Resolve runs on
// every API call, and the index is a few hundred kilobytes of JSON.
var loadIndex = sync.OnceValues(schemaindex.Embedded)

// registered indexes the registry by method id, once, for Resolve's membership
// check.
var registered = sync.OnceValue(func() map[string]bool {
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[e.MethodID] = true
	}
	return out
})
