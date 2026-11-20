package listing

import (
	"sort"
	"unicode/utf8"
)

type Field int

const (
	Title Field = iota
	ShortDescription
	FullDescription
	Video
)

type Spec struct {
	Field    Field
	File     string
	Key      string
	MaxChars int
	Required bool
}

var specs = []Spec{
	{Field: Title, File: "title.txt", Key: "title", MaxChars: 30, Required: true},
	{Field: ShortDescription, File: "short_description.txt", Key: "shortDescription", MaxChars: 80, Required: false},
	{Field: FullDescription, File: "full_description.txt", Key: "fullDescription", MaxChars: 4000, Required: true},
	{Field: Video, File: "video.txt", Key: "video", MaxChars: 0, Required: false},
}

func Specs() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

func Fields() []Field {
	out := make([]Field, len(specs))
	for i, s := range specs {
		out[i] = s.Field
	}
	return out
}

func SpecOf(f Field) (Spec, bool) {
	for _, s := range specs {
		if s.Field == f {
			return s, true
		}
	}
	return Spec{}, false
}

func SpecByFile(name string) (Spec, bool) {
	for _, s := range specs {
		if s.File == name {
			return s, true
		}
	}
	return Spec{}, false
}

func SpecByKey(key string) (Spec, bool) {
	for _, s := range specs {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}

func CharCount(s string) int {
	return utf8.RuneCountInString(s)
}

type Listing struct {
	Locale string
	Fields map[Field]string
}

func NewListing(locale string) Listing {
	return Listing{Locale: locale, Fields: make(map[Field]string)}
}

func (l *Listing) Set(f Field, v string) {
	if l.Fields == nil {
		l.Fields = make(map[Field]string)
	}
	l.Fields[f] = v
}

func (l Listing) Get(f Field) (string, bool) {
	v, ok := l.Fields[f]
	return v, ok
}

func (l Listing) Empty() bool {
	return len(l.Fields) == 0
}

type Tree map[string]Listing

func (t Tree) Locales() []string {
	out := make([]string, 0, len(t))
	for loc := range t {
		out = append(out, loc)
	}
	sort.Strings(out)
	return out
}
