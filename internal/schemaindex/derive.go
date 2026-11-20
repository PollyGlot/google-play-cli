package schemaindex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type discoveryDoc struct {
	Name      string                       `json:"name"`
	Version   string                       `json:"version"`
	Revision  string                       `json:"revision"`
	RootURL   string                       `json:"rootUrl"`
	Resources map[string]discoveryResource `json:"resources"`
	Schemas   map[string]discoverySchema   `json:"schemas"`
}

type discoveryResource struct {
	Methods   map[string]discoveryMethod   `json:"methods"`
	Resources map[string]discoveryResource `json:"resources"`
}

type discoveryMethod struct {
	ID          string                    `json:"id"`
	HTTPMethod  string                    `json:"httpMethod"`
	FlatPath    string                    `json:"flatPath"`
	Path        string                    `json:"path"`
	Description string                    `json:"description"`
	Parameters  map[string]discoveryParam `json:"parameters"`
	Request     *discoveryRef             `json:"request"`
	Response    *discoveryRef             `json:"response"`
	MediaUpload *discoveryMediaUpload     `json:"mediaUpload"`
}

type discoveryMediaUpload struct {
	Protocols struct {
		Simple struct {
			Path string `json:"path"`
		} `json:"simple"`
	} `json:"protocols"`
}

type discoveryParam struct {
	Description string   `json:"description"`
	Location    string   `json:"location"`
	Required    bool     `json:"required"`
	Type        string   `json:"type"`
	Enum        []string `json:"enum"`
}

type discoveryRef struct {
	Ref string `json:"$ref"`
}

type discoverySchema struct {
	Description string                       `json:"description"`
	Properties  map[string]discoveryProperty `json:"properties"`
}

type discoveryProperty struct {
	Description string          `json:"description"`
	Type        string          `json:"type"`
	Ref         string          `json:"$ref"`
	Enum        []string        `json:"enum"`
	Required    bool            `json:"required"`
	Items       *discoveryItems `json:"items"`
}

type discoveryItems struct {
	Ref  string `json:"$ref"`
	Type string `json:"type"`
}

func Derive(normalized []byte) (Index, error) {
	var doc discoveryDoc
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return Index{}, fmt.Errorf("parse discovery snapshot: %w", err)
	}

	idx := Index{
		Revision: doc.Revision,
		Methods:  map[string]Method{},
		Schemas:  map[string]Schema{},
	}

	prefix := basePathOf(doc)

	var walk func(rs map[string]discoveryResource)
	walk = func(rs map[string]discoveryResource) {
		for _, r := range rs {
			for _, m := range r.Methods {
				idx.Methods[m.ID] = deriveMethod(m, prefix)
			}
			walk(r.Resources)
		}
	}
	walk(doc.Resources)

	idx.Services = map[string]Service{
		doc.Name: {RootURL: doc.RootURL, BasePath: prefix},
	}

	for name, s := range doc.Schemas {
		idx.Schemas[name] = deriveSchema(s)
	}

	return idx, nil
}

func basePathOf(doc discoveryDoc) string {
	prefix := doc.Name + "/" + doc.Version + "/"
	seen, all := false, true
	var walk func(rs map[string]discoveryResource)
	walk = func(rs map[string]discoveryResource) {
		for _, r := range rs {
			for _, m := range r.Methods {
				seen = true
				if !strings.HasPrefix(m.FlatPath, prefix) {
					all = false
				}
			}
			walk(r.Resources)
		}
	}
	walk(doc.Resources)
	if !seen || !all {
		return ""
	}
	return prefix
}

func deriveMethod(m discoveryMethod, prefix string) Method {
	out := Method{
		HTTPMethod:  m.HTTPMethod,
		Path:        strings.TrimPrefix(m.FlatPath, prefix),
		Description: m.Description,
	}
	if m.Request != nil {
		out.Request = m.Request.Ref
	}
	if m.Response != nil {
		out.Response = m.Response.Ref
	}
	if m.MediaUpload != nil {
		out.UploadPath = strings.TrimPrefix(m.MediaUpload.Protocols.Simple.Path, "/")
	}
	for name, p := range m.Parameters {
		out.Parameters = append(out.Parameters, Param{
			Name:        name,
			In:          p.Location,
			Type:        p.Type,
			Enum:        p.Enum,
			Required:    p.Required,
			Description: p.Description,
		})
	}
	sort.Slice(out.Parameters, func(i, j int) bool {
		return out.Parameters[i].Name < out.Parameters[j].Name
	})
	return out
}

func deriveSchema(s discoverySchema) Schema {
	out := Schema{Description: s.Description}
	if len(s.Properties) == 0 {
		return out
	}
	out.Properties = make(map[string]Property, len(s.Properties))
	for name, p := range s.Properties {
		out.Properties[name] = deriveProperty(p)
	}
	return out
}

func deriveProperty(p discoveryProperty) Property {
	out := Property{
		Description: p.Description,
		Required:    p.Required,
	}
	switch {
	case p.Items != nil:
		out.Repeated = true
		if p.Items.Ref != "" {
			out.Ref = p.Items.Ref
		} else {
			out.Type = p.Items.Type
		}
	case p.Ref != "":
		out.Ref = p.Ref
	default:
		out.Type = p.Type
		out.Enum = p.Enum
	}
	return out
}

func Merge(indexes ...Index) Index {
	out := Index{
		Services: map[string]Service{},
		Methods:  map[string]Method{},
		Schemas:  map[string]Schema{},
	}
	for i, idx := range indexes {
		if i == 0 {
			out.Revision = idx.Revision
		}
		for name, svc := range idx.Services {
			out.Services[name] = svc
		}
		for id, m := range idx.Methods {
			out.Methods[id] = m
		}
		for name, s := range idx.Schemas {
			out.Schemas[name] = s
		}
	}
	return out
}

func marshal(idx Index) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		return nil, fmt.Errorf("encode schema index: %w", err)
	}
	return buf.Bytes(), nil
}

func Render(normalized []byte) ([]byte, error) {
	idx, err := Derive(normalized)
	if err != nil {
		return nil, err
	}
	return marshal(idx)
}

func RenderAll(snapshots [][]byte) ([]byte, error) {
	indexes := make([]Index, 0, len(snapshots))
	for _, snap := range snapshots {
		idx, err := Derive(snap)
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, idx)
	}
	return marshal(Merge(indexes...))
}
