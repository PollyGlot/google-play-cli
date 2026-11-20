package schemaindex

import (
	_ "embed"
	"encoding/json"
)

//go:embed schema_index.json
var embeddedJSON []byte

func Embedded() (Index, error) {
	return Load(embeddedJSON)
}

type Index struct {
	Revision string             `json:"revision,omitempty"`
	Services map[string]Service `json:"services,omitempty"`
	Methods  map[string]Method  `json:"methods"`
	Schemas  map[string]Schema  `json:"schemas"`
}

type Service struct {
	RootURL  string `json:"rootUrl,omitempty"`
	BasePath string `json:"basePath,omitempty"`
}

type Method struct {
	HTTPMethod  string  `json:"httpMethod"`
	Path        string  `json:"path"`
	Description string  `json:"description,omitempty"`
	Parameters  []Param `json:"parameters,omitempty"`
	Request     string  `json:"request,omitempty"`
	Response    string  `json:"response,omitempty"`
	UploadPath  string  `json:"uploadPath,omitempty"`
}

type Param struct {
	Name        string   `json:"name"`
	In          string   `json:"in"`
	Type        string   `json:"type,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Description string   `json:"description,omitempty"`
}

type Schema struct {
	Description string              `json:"description,omitempty"`
	Properties  map[string]Property `json:"properties,omitempty"`
}

type Property struct {
	Type        string   `json:"type,omitempty"`
	Ref         string   `json:"$ref,omitempty"`
	Repeated    bool     `json:"repeated,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Description string   `json:"description,omitempty"`
}

func Load(data []byte) (Index, error) {
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return Index{}, err
	}
	return idx, nil
}
