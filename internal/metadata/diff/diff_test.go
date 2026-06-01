package diff_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/diff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
)

// loc builds a single-locale tree with the given field→value pairs, each
// marked managed (present). Use it for both the local and online sides.
func loc(code string, kv map[listing.Field]string) listing.Tree {
	l := listing.NewListing(code)
	for f, v := range kv {
		l.Set(f, v)
	}
	return listing.Tree{code: l}
}

// findChange returns the Change for (locale, field) and whether it exists.
// field "" matches a locale-level record.
func findChange(r diff.Result, locale, field string) (diff.Change, bool) {
	for _, c := range r.Changes {
		if c.Locale == locale && c.Field == field {
			return c, true
		}
	}
	return diff.Change{}, false
}

// TestCompute_fieldClassification is the exhaustive table over a single
// field's local×online states, asserting the op and the summary bucket.
func TestCompute_fieldClassification(t *testing.T) {
	const code = "en-US"
	f := listing.Title
	key := "title"

	cases := []struct {
		name        string
		localSet    bool // is the field managed on disk?
		localVal    string
		onlineSet   bool // does the locale exist online with this field non-empty?
		onlineVal   string
		wantOp      diff.Op // "" => no change record emitted
		wantSummary func(diff.Summary) int
	}{
		{"create", true, "Hello", false, "", diff.OpCreate, func(s diff.Summary) int { return s.Create }},
		{"update", true, "Hello", true, "Hi", diff.OpUpdate, func(s diff.Summary) int { return s.Update }},
		{"unchanged_value", true, "Same", true, "Same", "", func(s diff.Summary) int { return s.Unchanged }},
		{"clear", true, "", true, "Online", diff.OpClear, func(s diff.Summary) int { return s.Clear }},
		{"clear_already_absent_noop", true, "", false, "", "", func(s diff.Summary) int { return s.Unchanged }},
		{"unmanaged_skipped", false, "", true, "Online", "", func(s diff.Summary) int { return 0 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local := listing.Tree{code: listing.NewListing(code)}
			if tc.localSet {
				l := local[code]
				l.Set(f, tc.localVal)
				local[code] = l
			}
			online := listing.Tree{code: listing.NewListing(code)}
			if tc.onlineSet {
				o := online[code]
				o.Set(f, tc.onlineVal)
				online[code] = o
			}

			r := diff.Compute("com.x", local, online, false)

			c, ok := findChange(r, code, key)
			if tc.wantOp == "" {
				if ok {
					t.Errorf("expected no change record, got %+v", c)
				}
			} else {
				if !ok {
					t.Fatalf("expected a %s record, got none (changes=%+v)", tc.wantOp, r.Changes)
				}
				if c.Op != tc.wantOp {
					t.Errorf("op = %q, want %q", c.Op, tc.wantOp)
				}
			}
			if got := tc.wantSummary(r.Summary); tc.name != "unmanaged_skipped" && got != 1 {
				t.Errorf("summary bucket = %d, want 1 (summary=%+v)", got, r.Summary)
			}
		})
	}
}

// TestCompute_charCounts asserts the live/local char counters, including the
// genuine zeros a create (live=0) and a clear (local=0) must carry.
func TestCompute_charCounts(t *testing.T) {
	local := loc("fr-FR", map[listing.Field]string{
		listing.FullDescription:  "abcd", // 4 chars
		listing.ShortDescription: "",     // clear
	})
	online := loc("fr-FR", map[listing.Field]string{
		listing.FullDescription:  "ab",  // 2 chars online -> update
		listing.ShortDescription: "xyz", // 3 chars online -> clear
		listing.Title:            "T",   // online only, unmanaged on disk -> ignored
	})

	r := diff.Compute("com.x", local, online, false)

	upd, ok := findChange(r, "fr-FR", "fullDescription")
	if !ok || upd.Op != diff.OpUpdate {
		t.Fatalf("fullDescription: got %+v, ok=%v; want update", upd, ok)
	}
	if upd.LiveChars == nil || *upd.LiveChars != 2 || upd.LocalChars == nil || *upd.LocalChars != 4 {
		t.Errorf("update chars = live %v local %v, want live 2 local 4", upd.LiveChars, upd.LocalChars)
	}

	clr, ok := findChange(r, "fr-FR", "shortDescription")
	if !ok || clr.Op != diff.OpClear {
		t.Fatalf("shortDescription: got %+v, ok=%v; want clear", clr, ok)
	}
	if clr.LiveChars == nil || *clr.LiveChars != 3 || clr.LocalChars == nil || *clr.LocalChars != 0 {
		t.Errorf("clear chars = live %v local %v, want live 3 local 0", clr.LiveChars, clr.LocalChars)
	}

	// title is online-only within a locale present on disk -> never emitted.
	if c, ok := findChange(r, "fr-FR", "title"); ok {
		t.Errorf("title should be ignored (unmanaged on disk), got %+v", c)
	}
}

// TestCompute_createCharCounts pins the create record's live=0 + local=count.
func TestCompute_createCharCounts(t *testing.T) {
	local := loc("de-DE", map[listing.Field]string{listing.Title: "Hallo"}) // 5
	online := listing.Tree{}                                                // no online locale at all

	r := diff.Compute("com.x", local, online, false)
	c, ok := findChange(r, "de-DE", "title")
	if !ok || c.Op != diff.OpCreate {
		t.Fatalf("got %+v ok=%v, want create", c, ok)
	}
	if c.LiveChars == nil || *c.LiveChars != 0 || c.LocalChars == nil || *c.LocalChars != 5 {
		t.Errorf("create chars = live %v local %v, want live 0 local 5", c.LiveChars, c.LocalChars)
	}
}

// TestCompute_untouchedLocale asserts an online-only locale is reported
// untouchedLocale (additive default) with a reason and no field, and that
// it never alters HasChanges.
func TestCompute_untouchedLocale(t *testing.T) {
	local := loc("en-US", map[listing.Field]string{listing.Title: "Hi", listing.FullDescription: "Long"})
	online := listing.Tree{
		"en-US": local["en-US"], // identical -> unchanged
		"es-ES": mustListing("es-ES", listing.Title, "Hola", listing.FullDescription, "Larga"),
	}

	r := diff.Compute("com.x", local, online, false)

	c, ok := findChange(r, "es-ES", "")
	if !ok || c.Op != diff.OpUntouchedLocale {
		t.Fatalf("es-ES: got %+v ok=%v, want untouchedLocale", c, ok)
	}
	if c.Reason == "" {
		t.Error("untouchedLocale record should carry a reason")
	}
	if c.LiveChars != nil || c.LocalChars != nil {
		t.Error("locale-level record should omit char counts")
	}
	if r.Summary.UntouchedLocales != 1 {
		t.Errorf("UntouchedLocales = %d, want 1", r.Summary.UntouchedLocales)
	}
	if r.HasChanges() {
		t.Error("HasChanges() = true with only unchanged+untouchedLocale, want false")
	}
}

// TestCompute_prune asserts that under --prune an online-only locale becomes
// a delete record counted in Summary.Delete (and HasChanges() true), while
// without prune the same input yields untouchedLocale.
func TestCompute_prune(t *testing.T) {
	local := loc("en-US", map[listing.Field]string{listing.Title: "Hi", listing.FullDescription: "Long"})
	online := listing.Tree{
		"en-US": local["en-US"],
		"it-IT": mustListing("it-IT", listing.Title, "Ciao", listing.FullDescription, "Lunga"),
	}

	withPrune := diff.Compute("com.x", local, online, true)
	c, ok := findChange(withPrune, "it-IT", "")
	if !ok || c.Op != diff.OpDelete {
		t.Fatalf("it-IT under prune: got %+v ok=%v, want delete", c, ok)
	}
	if withPrune.Summary.Delete != 1 || withPrune.Summary.UntouchedLocales != 0 {
		t.Errorf("prune summary = %+v, want Delete 1 UntouchedLocales 0", withPrune.Summary)
	}
	if !withPrune.HasChanges() {
		t.Error("HasChanges() = false under a prune delete, want true")
	}

	noPrune := diff.Compute("com.x", local, online, false)
	if c, _ := findChange(noPrune, "it-IT", ""); c.Op != diff.OpUntouchedLocale {
		t.Errorf("it-IT without prune: op = %q, want untouchedLocale", c.Op)
	}
}

// TestCompute_summaryTally runs a mixed tree and asserts every counter and
// the overall change list at once.
func TestCompute_summaryTally(t *testing.T) {
	local := listing.Tree{
		"en-US": mustListing("en-US",
			listing.Title, "New Title", // create (online has no title)
			listing.FullDescription, "Same", // unchanged
			listing.ShortDescription, ""), // clear (online has short)
		"fr-FR": mustListing("fr-FR",
			listing.Title, "Bonjour", // update
			listing.FullDescription, "Desc"), // unchanged
	}
	online := listing.Tree{
		"en-US": mustListing("en-US",
			listing.FullDescription, "Same",
			listing.ShortDescription, "online short"),
		"fr-FR": mustListing("fr-FR",
			listing.Title, "Salut",
			listing.FullDescription, "Desc"),
		"de-DE": mustListing("de-DE", listing.Title, "Hallo"), // untouchedLocale
	}

	r := diff.Compute("com.x", local, online, false)
	want := diff.Summary{Create: 1, Update: 1, Clear: 1, Unchanged: 2, UntouchedLocales: 1, Delete: 0}
	if r.Summary != want {
		t.Errorf("summary = %+v, want %+v", r.Summary, want)
	}
	// Changes list excludes unchanged: create + update + clear + untouched = 4.
	if len(r.Changes) != 4 {
		t.Errorf("len(Changes) = %d, want 4 (no unchanged listed): %+v", len(r.Changes), r.Changes)
	}
}

// TestCompute_deterministicOrder asserts locales come out lexically and
// fields in canonical order, so output is stable across runs.
func TestCompute_deterministicOrder(t *testing.T) {
	local := listing.Tree{
		"fr-FR": mustListing("fr-FR", listing.FullDescription, "f", listing.Title, "t"),
		"de-DE": mustListing("de-DE", listing.Title, "d"),
	}
	online := listing.Tree{}

	r := diff.Compute("com.x", local, online, false)
	// de-DE before fr-FR; within fr-FR, title (canonical idx 0) before
	// fullDescription (idx 2).
	var order []string
	for _, c := range r.Changes {
		order = append(order, c.Locale+"/"+c.Field)
	}
	want := []string{"de-DE/title", "fr-FR/title", "fr-FR/fullDescription"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
}

// mustListing builds a Listing with alternating field,value varargs.
func mustListing(code string, fv ...any) listing.Listing {
	l := listing.NewListing(code)
	for i := 0; i+1 < len(fv); i += 2 {
		l.Set(fv[i].(listing.Field), fv[i+1].(string))
	}
	return l
}
