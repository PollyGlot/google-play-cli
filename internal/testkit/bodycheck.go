package testkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// route is one Discovery method that takes a request body: the URL it answers
// on and the schema its body follows.
type route struct {
	id      string
	verb    string
	pattern *regexp.Regexp // host and path; a placeholder matches one segment
	literal int            // literal characters of the template: the most specific match wins, so `{productId}` does not take `{productId}:archive`
	schema  string
}

// checker holds what checking a body needs, derived once from the embedded
// Schema index: Google's own description of every request, never a second
// hand-kept table.
type checker struct {
	routes   []route
	schemas  map[string]schemaindex.Schema
	bySchema map[string][]string // schema name -> rule keys
}

var bodyChecker = sync.OnceValue(func() *checker {
	idx, err := schemaindex.Embedded()
	if err != nil {
		panic(fmt.Sprintf("testkit: load the embedded Schema index: %v", err))
	}
	c := &checker{schemas: idx.Schemas, bySchema: map[string][]string{}}
	for key := range rules {
		schema, _, _ := strings.Cut(key, ".")
		c.bySchema[schema] = append(c.bySchema[schema], key)
	}
	for _, keys := range c.bySchema {
		sort.Strings(keys)
	}
	for id, m := range idx.Methods {
		if m.Request == "" {
			continue
		}
		service, _, _ := strings.Cut(id, ".")
		svc := idx.Services[service]
		root := strings.TrimPrefix(svc.RootURL, "https://")
		c.routes = append(c.routes, newRoute(id, m.HTTPMethod, root+svc.BasePath+m.Path, m.Request))
		if m.UploadPath != "" {
			c.routes = append(c.routes, newRoute(id, m.HTTPMethod, root+m.UploadPath, m.Request))
		}
	}
	// Sorted so a tie between equally specific templates resolves the same
	// way on every run.
	sort.Slice(c.routes, func(i, j int) bool { return c.routes[i].id < c.routes[j].id })
	return c
})

// placeholder matches a `{name}` path parameter of a Discovery flatPath.
var placeholder = regexp.MustCompile(`\{[^}]+\}`)

func newRoute(id, verb, tmpl, schema string) route {
	var re strings.Builder
	re.WriteString("^")
	literal, last := 0, 0
	for _, loc := range placeholder.FindAllStringIndex(tmpl, -1) {
		re.WriteString(regexp.QuoteMeta(tmpl[last:loc[0]]))
		re.WriteString(`[^/]+`)
		literal += loc[0] - last
		last = loc[1]
	}
	re.WriteString(regexp.QuoteMeta(tmpl[last:]) + "$")
	literal += len(tmpl) - last
	return route{id: id, verb: verb, pattern: regexp.MustCompile(re.String()), literal: literal, schema: schema}
}

// violation is one rule a request body breaks.
type violation struct {
	key string // Schema.property
	at  string // JSON path of the object holding the property, "" for the root
	why string
}

// check matches c to the Discovery method it calls and walks its JSON body
// against the rules. A request no method takes a body on, or whose body is
// not a JSON object (media bytes), has nothing to check.
func (k *checker) check(c Call) (string, []violation) {
	trimmed := bytes.TrimSpace(c.Body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", nil
	}
	// The escaped path keeps a parameter one segment even when its value holds
	// a slash (a review ID, an `applications/<pkg>` name).
	u, err := url.Parse(c.URL)
	if err != nil {
		return "", nil
	}
	target := c.Host + u.EscapedPath()
	var best *route
	for i, r := range k.routes {
		if r.verb == c.Method && r.pattern.MatchString(target) && (best == nil || r.literal > best.literal) {
			best = &k.routes[i]
		}
	}
	if best == nil {
		return "", nil
	}
	var body any
	if err := json.Unmarshal(trimmed, &body); err != nil {
		return best.id, nil
	}
	var out []violation
	k.walk(best.schema, body, "", &out)
	return best.id, out
}

func (k *checker) walk(schema string, v any, at string, out *[]violation) {
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	for _, key := range k.bySchema[schema] {
		if why := rules[key].check(obj); why != "" {
			*out = append(*out, violation{key: key, at: at, why: why})
		}
	}
	names := make([]string, 0, len(obj))
	for name := range obj {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := k.schemas[schema].Properties[name]
		if p.Ref == "" {
			continue
		}
		path := name
		if at != "" {
			path = at + "." + name
		}
		if !p.Repeated {
			k.walk(p.Ref, obj[name], path, out)
			continue
		}
		items, _ := obj[name].([]any)
		for i, item := range items {
			k.walk(p.Ref, item, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// refusal checks c and describes the rules its body breaks, those in allowed
// excepted, for a test failure; "" when it breaks none.
func refusal(c Call, allowed map[string]bool) string {
	id, vs := bodyChecker().check(c)
	var (
		b     strings.Builder
		first string
	)
	for _, v := range vs {
		if allowed[v.key] {
			continue
		}
		if first == "" {
			first = v.key
		}
		at := "body"
		if v.at != "" {
			at = v.at
		}
		fmt.Fprintf(&b, "\n  %s at %s: %s\n    Discovery: %q", v.key, at, v.why, rules[v.key].sentence)
	}
	if first == "" {
		return ""
	}
	return fmt.Sprintf("testkit: Play refuses this %s body (%s %s):%s\n  body: %s\n"+
		"A test that sends it on purpose exempts the rule with AllowInvalid(%q).",
		id, c.Method, c.URL, b.String(), c.Body, first)
}

// allowList validates the rule keys a test exempts: a typo would otherwise
// exempt nothing and the test would fail for a reason it claims to handle.
func allowList(set map[string]bool, keys []string) {
	if len(keys) == 0 {
		panic("testkit: AllowInvalid needs the rule keys it exempts")
	}
	for _, k := range keys {
		if _, ok := rules[k]; !ok {
			panic(fmt.Sprintf("testkit: AllowInvalid(%q): no such rule", k))
		}
		set[k] = true
	}
}
