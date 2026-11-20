package apiregistry

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

type Method struct {
	ID             string
	Verb           string
	URLTemplate    string
	UploadTemplate string
	Idempotent     bool
	Paginated      bool
}

func (m Method) URL(params map[string]string) (string, error) {
	return expand(m.ID, m.URLTemplate, params)
}

func (m Method) UploadURL(params map[string]string) (string, error) {
	if m.UploadTemplate == "" {
		return "", fmt.Errorf("api method %s: no media-upload endpoint", m.ID)
	}
	return expand(m.ID, m.UploadTemplate, params)
}

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
		Idempotent:  idempotent(id, m.HTTPMethod),
		Paginated:   paginated(idx, m),
	}
	if m.UploadPath != "" {
		out.UploadTemplate = svc.RootURL + m.UploadPath
	}
	return out, nil
}

func MustResolve(id string) Method {
	m, err := Resolve(id)
	if err != nil {
		panic(err)
	}
	return m
}

func serviceOf(id string) string {
	if i := strings.IndexByte(id, '.'); i > 0 {
		return id[:i]
	}
	return id
}

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

var loadIndex = sync.OnceValues(schemaindex.Embedded)

var registered = sync.OnceValue(func() map[string]bool {
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[e.MethodID] = true
	}
	return out
})
