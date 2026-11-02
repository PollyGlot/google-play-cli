package testkit

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// constraintWording finds the Discovery descriptions that state a rule on a
// property in prose (#735). It is deliberately wider than the rules table: a
// match the table does not encode must be accounted for below.
var constraintWording = regexp.MustCompile(`(?i)can only be|only be set|must not be|strictly (smaller|larger|greater|less)|0 <|required (when|if)|cannot be (set|specified|used)|only (valid|allowed|applicable|supported) (when|if|for)|must be (set|specified|empty|unset) (when|if)|should only be|may only be|only when|only if|not be set|mutually exclusive|at most one of|exactly one of`)

// unenforced lists the properties gplay writes whose constraint the table
// does not check, each with the Discovery sentence it rests on and why.
var unenforced = map[string]struct{ sentence, why string }{
	"ExpansionFile.referencesVersion": {
		sentence: "The file_size field will not be set.",
		why:      "describes the response: Play fills fileSize, and gplay's request carries referencesVersion alone",
	},
	"OtherRegionsSubscriptionOfferPhaseConfig.otherRegionsPrices": {
		sentence: "The price must not be smaller than the minimum price allowed for any new locations Play may launch in.",
		why:      "the minimum price is Play's server-side data, absent from Discovery",
	},
	"RegionalSubscriptionOfferPhaseConfig.price": {
		sentence: "The price must not be smaller than the minimum price allowed for this region.",
		why:      "the minimum price is Play's server-side data, absent from Discovery",
	},
}

func embeddedIndex(t *testing.T) schemaindex.Index {
	t.Helper()
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("embedded Schema index: %v", err)
	}
	return idx
}

// description returns the Discovery description of a `Schema.property` key.
func description(t *testing.T, idx schemaindex.Index, key string) string {
	t.Helper()
	schema, prop, _ := strings.Cut(key, ".")
	p, ok := idx.Schemas[schema].Properties[prop]
	if !ok {
		t.Fatalf("%s: no such property in the Schema index", key)
	}
	return p.Description
}

// written returns the schemas a request body of a registered method can
// hold: what gplay may send, as opposed to what it only reads.
func written(idx schemaindex.Index) map[string]string {
	via := map[string]string{}
	var walk func(schema, id string)
	walk = func(schema, id string) {
		if _, seen := via[schema]; seen {
			return
		}
		via[schema] = id
		for _, p := range idx.Schemas[schema].Properties {
			if p.Ref != "" {
				walk(p.Ref, id)
			}
		}
	}
	for _, e := range apiregistry.Entries() {
		if req := idx.Methods[e.MethodID].Request; req != "" {
			walk(req, e.MethodID)
		}
	}
	return via
}

// A refresh that rewords a sentence a rule or an exclusion rests on fails
// here, so the rule is reviewed against Google's new wording.
func TestRules_anchorTheirDiscoverySentence(t *testing.T) {
	idx := embeddedIndex(t)
	for key, r := range rules {
		if d := description(t, idx, key); !strings.Contains(d, r.sentence) {
			t.Errorf("%s: Discovery no longer says %q; review the rule against the new description:\n%s", key, r.sentence, d)
		}
	}
	for key, u := range unenforced {
		if d := description(t, idx, key); !strings.Contains(d, u.sentence) {
			t.Errorf("%s: Discovery no longer says %q; review the exclusion against the new description:\n%s", key, u.sentence, d)
		}
	}
}

// Every property whose description states a constraint is in the table, is
// excluded with a reason, or sits in a schema no request gplay sends can hold.
func TestRules_inventoryCoversEveryDocumentedConstraint(t *testing.T) {
	idx := embeddedIndex(t)
	via := written(idx)
	var notWritten []string
	for schema, s := range idx.Schemas {
		for prop, p := range s.Properties {
			key := schema + "." + prop
			if !constraintWording.MatchString(p.Description) {
				continue
			}
			_, ruled := rules[key]
			_, excluded := unenforced[key]
			switch id, sent := via[schema]; {
			case ruled || excluded:
			case sent:
				t.Errorf("%s: gplay writes it (%s) and Discovery states a constraint; add a rule to the table or an entry to unenforced with the reason:\n%s", key, id, p.Description)
			default:
				notWritten = append(notWritten, key)
			}
		}
	}
	sort.Strings(notWritten)
	t.Logf("constraints on properties no gplay request carries: %s", strings.Join(notWritten, ", "))

	for key := range rules {
		if schema, _, _ := strings.Cut(key, "."); via[schema] == "" {
			t.Errorf("rule %s: no registered method sends %s, so the rule checks nothing", key, schema)
		}
	}
	for key := range unenforced {
		if schema, _, _ := strings.Cut(key, "."); via[schema] == "" {
			t.Errorf("unenforced %s: no registered method sends %s any more; drop the entry", key, schema)
		}
	}
}
