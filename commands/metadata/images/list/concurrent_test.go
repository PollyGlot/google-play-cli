package imageslist_test

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"testing"
	"time"

	imageslist "github.com/PollyGlot/google-play-cli/commands/metadata/images/list"
	"github.com/PollyGlot/google-play-cli/internal/fanout"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRun_concurrentListKeepsSlotOrder: the 9 x L images.list reads run
// fanout.Limit at a time and answer in a scrambled order, yet the slots come
// out in (sorted locale, canonical type) order, the order a serial walk gives.
func TestRun_concurrentListKeepsSlotOrder(t *testing.T) {
	locales := []string{"de-DE", "en-US", "es-ES", "fr-FR", "it-IT"}
	// Every slot but tvBanner holds one image, so an ordering slip anywhere in
	// the 9 x 5 grid shows.
	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 0, `{"id":"e1"}`, true
		case c.Method == http.MethodDelete && strings.HasSuffix(c.Path, "/edits/e1"):
			return http.StatusNoContent, "", true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/listings"):
			var ls []string
			// Deliberately unsorted: list sorts the locales itself.
			for i := len(locales) - 1; i >= 0; i-- {
				ls = append(ls, fmt.Sprintf(`{"language":%q}`, locales[i]))
			}
			return 0, `{"listings":[` + strings.Join(ls, ",") + `]}`, true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/listings/"):
			slot := strings.Split(c.Path, "/listings/")[1]
			if strings.HasSuffix(slot, "/"+string(images.TvBanner)) {
				return 0, `{"images":[]}`, true
			}
			return 0, fmt.Sprintf(`{"images":[{"id":%q,"url":"u","sha256":%q}]}`, slot, slot), true
		}
		return 0, "", false
	})
	// A per-slot pseudo-random latency scrambles the completion order.
	rt := testkit.NewDelayed(fake, func(r *http.Request) time.Duration {
		h := fnv.New32a()
		_, _ = h.Write([]byte(r.URL.Path))
		return time.Duration(1+h.Sum32()%6) * time.Millisecond
	})
	rc := newRC(t, rt)

	out, err := imageslist.Run(rc, imageslist.Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p := rt.Peak(); p != fanout.Limit {
		t.Errorf("peak in-flight requests = %d, want exactly fanout.Limit (%d)", p, fanout.Limit)
	}
	var want []string
	for _, loc := range locales {
		for _, ty := range images.Types() {
			if ty != images.TvBanner {
				want = append(want, loc+"/"+string(ty))
			}
		}
	}
	slots := out.(imageslist.Payload).Slots
	if len(slots) != len(want) {
		t.Fatalf("got %d slots, want %d", len(slots), len(want))
	}
	for i, s := range slots {
		got := s.Locale + "/" + string(s.Type)
		if got != want[i] || s.Images[0].ID != want[i] {
			t.Errorf("slot %d = %s (image %s), want %s", i, got, s.Images[0].ID, want[i])
		}
	}
}
