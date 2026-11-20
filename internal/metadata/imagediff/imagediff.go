package imagediff

import (
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

type Op string

const (
	OpUpload        Op = "upload"
	OpDelete        Op = "delete"
	OpReorder       Op = "reorder"
	OpUnchanged     Op = "unchanged"
	OpUntouchedSlot Op = "untouchedSlot"
)

type Kind int

const (
	KindNone Kind = iota
	KindAppend
	KindRewrite
	KindDeleteIDs
)

type SlotChange struct {
	Locale    string `json:"locale"`
	ImageType string `json:"imageType"`
	Op        Op     `json:"op"`
	Sha256    string `json:"sha256,omitempty"`
	Position  int    `json:"position"`
}

type Summary struct {
	Upload    int `json:"upload"`
	Delete    int `json:"delete"`
	Reorder   int `json:"reorder"`
	Unchanged int `json:"unchanged"`
}

type Result struct {
	Package string       `json:"package"`
	Slots   []SlotChange `json:"slots"`
	Summary Summary      `json:"summary"`
}

func (r Result) HasChanges() bool {
	return r.Summary.Upload+r.Summary.Delete+r.Summary.Reorder > 0
}

type SlotInput struct {
	Locale string
	Type   images.Type
	Local  []string
	Live   []images.Image
}

type Plan struct {
	Locale    string
	Type      images.Type
	Changes   []SlotChange
	Kind      Kind
	Uploads   []int
	DeleteIDs []string
}

func Plans(slots []SlotInput, prune bool) []Plan {
	out := make([]Plan, 0, len(slots))
	for _, in := range slots {
		out = append(out, reconcile(in, prune))
	}
	return out
}

func Aggregate(pkg string, plans []Plan) Result {
	res := Result{Package: pkg}
	for _, p := range plans {
		for _, c := range p.Changes {
			res.Slots = append(res.Slots, c)
			switch c.Op {
			case OpUpload:
				res.Summary.Upload++
			case OpDelete:
				res.Summary.Delete++
			case OpReorder:
				res.Summary.Reorder++
			case OpUnchanged:
				res.Summary.Unchanged++
			case OpUntouchedSlot:
			}
		}
	}
	return res
}

func reconcile(in SlotInput, prune bool) Plan {
	p := Plan{Locale: in.Locale, Type: in.Type}
	localShas := in.Local
	liveShas := liveShasOf(in.Live)

	if len(in.Local) == 0 {
		if len(in.Live) > 0 {
			p.Changes = []SlotChange{slotLevel(in, OpUntouchedSlot)}
		}
		return p
	}

	if equalStrings(localShas, liveShas) {
		p.Changes = []SlotChange{slotLevel(in, OpUnchanged)}
		return p
	}

	liveSet := toSet(liveShas)

	if !in.Type.Gallery() {
		p.Kind = KindRewrite
		p.Uploads = allIndices(localShas)
		p.Changes = []SlotChange{{Locale: in.Locale, ImageType: string(in.Type), Op: OpUpload, Sha256: localShas[0], Position: 0}}
		return p
	}

	onlineOnly := imagesAbsentFrom(in.Live, toSet(localShas))

	if len(onlineOnly) == 0 {
		if isPrefix(liveShas, localShas) {
			p.Kind = KindAppend
			for i := len(liveShas); i < len(localShas); i++ {
				p.Uploads = append(p.Uploads, i)
				p.Changes = append(p.Changes, uploadChange(in, localShas[i], i))
			}
			return p
		}
		p.Kind = KindRewrite
		p.Uploads = allIndices(localShas)
		p.Changes = append(p.Changes, slotLevel(in, OpReorder))
		for i, s := range localShas {
			if !liveSet[s] {
				p.Changes = append(p.Changes, uploadChange(in, s, i))
			}
		}
		return p
	}

	if prune {
		return prunePlan(in, localShas, liveShas, liveSet, onlineOnly)
	}

	for i, s := range localShas {
		if !liveSet[s] {
			p.Uploads = append(p.Uploads, i)
			p.Changes = append(p.Changes, uploadChange(in, s, i))
		}
	}
	if len(p.Uploads) > 0 {
		p.Kind = KindAppend
	} else {
		p.Changes = []SlotChange{slotLevel(in, OpUnchanged)}
	}
	return p
}

func prunePlan(in SlotInput, localShas, liveShas []string, liveSet map[string]bool, onlineOnly []images.Image) Plan {
	p := Plan{Locale: in.Locale, Type: in.Type}
	localSet := toSet(localShas)

	var newLocal []int
	for i, s := range localShas {
		if !liveSet[s] {
			newLocal = append(newLocal, i)
		}
	}
	retainedLive := keepInSet(liveShas, localSet)
	retainedLocal := keepInSet(localShas, liveSet)
	reordered := !equalStrings(retainedLive, retainedLocal)

	if len(newLocal) == 0 && !reordered {
		p.Kind = KindDeleteIDs
		for _, img := range onlineOnly {
			p.DeleteIDs = append(p.DeleteIDs, img.ID)
			p.Changes = append(p.Changes, SlotChange{Locale: in.Locale, ImageType: string(in.Type), Op: OpDelete, Sha256: img.Sha256, Position: -1})
		}
		return p
	}

	p.Kind = KindRewrite
	p.Uploads = allIndices(localShas)
	if reordered {
		p.Changes = append(p.Changes, slotLevel(in, OpReorder))
	}
	for _, i := range newLocal {
		p.Changes = append(p.Changes, uploadChange(in, localShas[i], i))
	}
	for _, img := range onlineOnly {
		p.Changes = append(p.Changes, SlotChange{Locale: in.Locale, ImageType: string(in.Type), Op: OpDelete, Sha256: img.Sha256, Position: -1})
	}
	return p
}

func keepInSet(ss []string, set map[string]bool) []string {
	var out []string
	for _, s := range ss {
		if set[s] {
			out = append(out, s)
		}
	}
	return out
}

func slotLevel(in SlotInput, op Op) SlotChange {
	return SlotChange{Locale: in.Locale, ImageType: string(in.Type), Op: op, Position: -1}
}

func uploadChange(in SlotInput, sha string, pos int) SlotChange {
	return SlotChange{Locale: in.Locale, ImageType: string(in.Type), Op: OpUpload, Sha256: sha, Position: pos}
}

func allIndices(seq []string) []int {
	out := make([]int, len(seq))
	for i := range seq {
		out[i] = i
	}
	return out
}

func liveShasOf(live []images.Image) []string {
	out := make([]string, len(live))
	for i, img := range live {
		out[i] = img.Sha256
	}
	return out
}

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isPrefix(short, long []string) bool {
	if len(short) > len(long) {
		return false
	}
	for i := range short {
		if short[i] != long[i] {
			return false
		}
	}
	return true
}

func imagesAbsentFrom(live []images.Image, set map[string]bool) []images.Image {
	var out []images.Image
	for _, img := range live {
		if !set[img.Sha256] {
			out = append(out, img)
		}
	}
	return out
}
