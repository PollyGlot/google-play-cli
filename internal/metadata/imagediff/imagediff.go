// Package imagediff is the Store-image reconciliation engine: the pure
// comparison of a local image sequence (files sorted by name, by sha256)
// against the live sequence (images.list, which carries a sha256 per image but
// no order field — display order is list order). It produces two things from
// one decision: the ADR-0013 §5 diff schema (for `--dry-run --output json`)
// and an executable per-slot Plan (for the orchestrator). It performs no I/O.
//
// Identity is by content (sha256), order-aware for galleries (ADR-0013 §1):
//
//   - identical sequence → no-op (unchanged).
//   - a managed slot with no live image, or a content change, uploads.
//   - a gallery that differs in order is reconciled by deleteall + re-upload in
//     name order (the only way to reorder), but ONLY when no online-only image
//     would be lost. Under the additive default (this slice, #134) an
//     online-only image — one live whose sha256 is in no local file — is never
//     deleted; apply only appends the genuinely-new local images. Removing
//     online-only images (full reconciliation, including reorder past a loss)
//     is --prune (#135).
//   - a slot absent/empty on disk but live on Play is untouchedSlot (missing ==
//     empty): left entirely alone.
package imagediff

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// Op is a per-slot reconciliation verb in the ADR-0013 §5 schema.
type Op string

const (
	OpUpload        Op = "upload"
	OpDelete        Op = "delete"
	OpReorder       Op = "reorder"
	OpUnchanged     Op = "unchanged"
	OpUntouchedSlot Op = "untouchedSlot"
)

// Kind is the executable action the orchestrator runs for a slot.
type Kind int

const (
	// KindNone: nothing to do (unchanged or untouchedSlot).
	KindNone Kind = iota
	// KindAppend: upload each blob in Uploads (images.upload appends in order).
	KindAppend
	// KindRewrite: deleteall the slot, then upload each blob in Uploads (the
	// full local sequence) in order — the only way to reorder or replace.
	KindRewrite
	// KindDeleteIDs: delete each live image id in DeleteIDs (a pure removal,
	// order preserved) — used only under --prune (#135).
	KindDeleteIDs
)

// SlotChange is one record in the ADR-0013 §5 diff schema. Position is the
// 0-based display position the change concerns, or -1 for a slot-level record
// (reorder, unchanged, untouchedSlot). A reordered gallery emits ONE reorder
// record for the slot plus upload/delete records for genuine adds/removes, so
// the summary stays honest.
type SlotChange struct {
	Locale    string `json:"locale"`
	ImageType string `json:"imageType"`
	Op        Op     `json:"op"`
	Sha256    string `json:"sha256,omitempty"`
	Position  int    `json:"position"`
}

// Summary is the flat counter tally — kept flat so a CI gate is one jq line:
// `.summary.upload + .summary.delete + .summary.reorder > 0`. Upload/Delete
// count images; Reorder/Unchanged count slots.
type Summary struct {
	Upload    int `json:"upload"`
	Delete    int `json:"delete"`
	Reorder   int `json:"reorder"`
	Unchanged int `json:"unchanged"`
}

// Result is the aggregated diff document (the ADR-0013 §5 schema). It keeps the
// text schema's shape (a flat outer object, a flat counter summary) with
// image-native fields.
type Result struct {
	Package string       `json:"package"`
	Slots   []SlotChange `json:"slots"`
	Summary Summary      `json:"summary"`
}

// HasChanges reports whether any genuine mutation is planned (the CI gate).
func (r Result) HasChanges() bool {
	return r.Summary.Upload+r.Summary.Delete+r.Summary.Reorder > 0
}

// SlotInput pairs one slot's local byte sequence (ordered, may be empty when
// unmanaged) with its live images (ordered as images.list returned them).
type SlotInput struct {
	Locale string
	Type   images.Type
	Local  [][]byte
	Live   []images.Image
}

// Plan is the per-slot reconciliation outcome: the schema Changes plus the
// executable action the orchestrator runs.
type Plan struct {
	Locale    string
	Type      images.Type
	Changes   []SlotChange
	Kind      Kind
	Uploads   [][]byte // KindAppend: blobs to append; KindRewrite: full local seq
	DeleteIDs []string // KindDeleteIDs: live image ids to delete (prune)
}

// Plans reconciles every slot independently (additive default — no prune).
func Plans(slots []SlotInput) []Plan {
	out := make([]Plan, 0, len(slots))
	for _, in := range slots {
		out = append(out, reconcile(in))
	}
	return out
}

// Aggregate flattens per-slot plans into the ADR-0013 §5 Result, tallying the
// summary from the emitted change ops (so re-uploads of unchanged images in a
// rewrite are not double-counted — only genuine adds/removes/reorders are).
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
			}
		}
	}
	return res
}

// reconcile computes one slot's plan under the additive model.
func reconcile(in SlotInput) Plan {
	p := Plan{Locale: in.Locale, Type: in.Type}
	localShas := shasOf(in.Local)
	liveShas := liveShasOf(in.Live)

	// Unmanaged: nothing on disk for this slot.
	if len(in.Local) == 0 {
		if len(in.Live) > 0 {
			p.Changes = []SlotChange{slotLevel(in, OpUntouchedSlot)}
		}
		return p
	}

	// Identical content and order → no-op.
	if equalStrings(localShas, liveShas) {
		p.Changes = []SlotChange{slotLevel(in, OpUnchanged)}
		return p
	}

	liveSet := toSet(liveShas)

	// Singular slot: the local file is the desired single image; any difference
	// is a replace (deleteall + upload).
	if !in.Type.Gallery() {
		p.Kind = KindRewrite
		p.Uploads = in.Local
		p.Changes = []SlotChange{{Locale: in.Locale, ImageType: string(in.Type), Op: OpUpload, Sha256: localShas[0], Position: 0}}
		return p
	}

	// Gallery slot.
	onlineOnly := imagesAbsentFrom(in.Live, toSet(localShas))

	if len(onlineOnly) == 0 {
		// No online-only image would be lost: reconcile fully to local.
		if isPrefix(liveShas, localShas) {
			// Pure append, order preserved: upload only the tail.
			p.Kind = KindAppend
			for i := len(liveShas); i < len(localShas); i++ {
				p.Uploads = append(p.Uploads, in.Local[i])
				p.Changes = append(p.Changes, uploadChange(in, localShas[i], i))
			}
			return p
		}
		// Order or content differs (set ⊆ local): rewrite = deleteall + reupload.
		p.Kind = KindRewrite
		p.Uploads = in.Local
		p.Changes = append(p.Changes, slotLevel(in, OpReorder))
		for i, s := range localShas {
			if !liveSet[s] {
				p.Changes = append(p.Changes, uploadChange(in, s, i))
			}
		}
		return p
	}

	// Online-only images exist. Additive default: never delete them; only
	// append the genuinely-new local images (those not already live). Full
	// reconciliation (which would drop the online-only ones) is --prune (#135).
	for i, s := range localShas {
		if !liveSet[s] {
			p.Uploads = append(p.Uploads, in.Local[i])
			p.Changes = append(p.Changes, uploadChange(in, s, i))
		}
	}
	if len(p.Uploads) > 0 {
		p.Kind = KindAppend
	} else {
		// Nothing to add additively (local ⊆ live); the online-only images are
		// kept — from the additive view the slot is unchanged.
		p.Changes = []SlotChange{slotLevel(in, OpUnchanged)}
	}
	return p
}

func slotLevel(in SlotInput, op Op) SlotChange {
	return SlotChange{Locale: in.Locale, ImageType: string(in.Type), Op: op, Position: -1}
}

func uploadChange(in SlotInput, sha string, pos int) SlotChange {
	return SlotChange{Locale: in.Locale, ImageType: string(in.Type), Op: OpUpload, Sha256: sha, Position: pos}
}

// shasOf hashes each local blob.
func shasOf(seq [][]byte) []string {
	out := make([]string, len(seq))
	for i, b := range seq {
		out[i] = hashOf(b)
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

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
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

// isPrefix reports whether short is an exact prefix of long (same elements,
// same order, from index 0).
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

// imagesAbsentFrom returns the live images whose sha256 is not in set.
func imagesAbsentFrom(live []images.Image, set map[string]bool) []images.Image {
	var out []images.Image
	for _, img := range live {
		if !set[img.Sha256] {
			out = append(out, img)
		}
	}
	return out
}
