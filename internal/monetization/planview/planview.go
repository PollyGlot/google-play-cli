package planview

import (
	"fmt"
	"io"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type Axis struct {
	command   string
	basePlans bool
	migrates  bool
}

var (
	Subscriptions   = Axis{command: "subscriptions apply", basePlans: true}
	OneTimeProducts = Axis{command: "iap apply", migrates: true}
)

type View struct {
	Axis      Axis
	Package   string
	Plan      reconcile.Plan
	Migrating map[string]bool
	DryRun    bool
	Requires  []string
}

var pastTense = map[string]string{
	"create":     "created",
	"migrate":    "migrated",
	"patch":      "patched",
	"delete":     "deleted",
	"activate":   "activated",
	"deactivate": "deactivated",
	"cancel":     "cancelled",
}

var kindLabel = map[string]string{
	reconcile.KindOffer:          "offer",
	reconcile.KindBasePlan:       "base plan",
	reconcile.KindPurchaseOption: "purchase option",
}

func (v View) verb(op string) string {
	if v.DryRun {
		return op
	}
	return pastTense[op]
}

func (v View) op(e reconcile.Entry) string {
	if e.Op == "create" && e.Kind == "" && v.Migrating[e.ProductID] {
		return "migrate"
	}
	return e.Op
}

func isStateOp(op string) bool {
	return op == "activate" || op == "deactivate" || op == "cancel"
}

func (v View) line(e reconcile.Entry) string {
	op := v.op(e)
	var s string
	switch {
	case op == "migrate":
		return fmt.Sprintf("%s %s (legacy → v2, one-way)", v.verb(op), e.ProductID)
	case isStateOp(op):
		return fmt.Sprintf("%s %s %s (%s → %s)", v.verb(op), kindLabel[e.Kind], e.Target(), e.From, e.To)
	case e.Kind == "":
		s = v.verb(op) + " " + e.ProductID
	default:
		s = v.verb(op) + " " + kindLabel[e.Kind] + " " + e.Target()
	}
	if op == "patch" {
		s += " (" + strings.Join(e.Fields, ", ") + ")"
	}
	return s
}

func (v View) summary() (keys []string, counts map[string]int) {
	p := v.Plan
	counts = map[string]int{
		"create":      len(p.Creates) - len(v.Migrating),
		"patch":       len(p.Patches),
		"delete":      len(p.Deletes),
		"offerCreate": len(p.OfferCreates),
		"offerPatch":  len(p.OfferPatches),
		"offerDelete": len(p.OfferDeletes),
		"state":       len(p.StateChanges),
		"unchanged":   len(p.Unchanged),
	}
	keys = []string{"create"}
	if v.Axis.migrates {
		keys = append(keys, "migrate")
		counts["migrate"] = len(v.Migrating)
	}
	keys = append(keys, "patch", "delete")
	if v.Axis.basePlans {
		keys = append(keys, "basePlanDelete")
		counts["basePlanDelete"] = len(p.BasePlanDeletes)
	}
	keys = append(keys, "offerCreate", "offerPatch", "offerDelete", "state", "unchanged")
	return keys, counts
}

func (v View) Human(w io.Writer, markdown bool) error {
	if markdown {
		if _, err := fmt.Fprintf(w, "## %s: %s\n\n", v.Axis.command, v.Package); err != nil {
			return err
		}
	}
	if !v.Plan.HasChanges() {
		_, err := fmt.Fprintln(w, "no changes to apply (catalog directory already matches Play)")
		return err
	}
	header := "applied to"
	if v.DryRun {
		header = "plan for"
	}
	entries := v.Plan.Entries()
	if _, err := fmt.Fprintf(w, "%s %s (%d change(s)):\n", header, v.Package, len(entries)); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := fmt.Fprintf(w, "  %s\n", v.line(e)); err != nil {
			return err
		}
	}
	keys, counts := v.summary()
	tally := make([]string, len(keys))
	for i, k := range keys {
		tally[i] = fmt.Sprintf("%s=%d", k, counts[k])
	}
	if _, err := fmt.Fprintf(w, "summary: %s\n", strings.Join(tally, " ")); err != nil {
		return err
	}
	if len(v.Requires) > 0 {
		if _, err := fmt.Fprintf(w, "requires: %s\n", strings.Join(v.Requires, ", ")); err != nil {
			return err
		}
	}
	return nil
}

type jsonChange struct {
	Op               string   `json:"op"`
	Kind             string   `json:"kind,omitempty"`
	ProductID        string   `json:"productId"`
	BasePlanID       string   `json:"basePlanId,omitempty"`
	PurchaseOptionID string   `json:"purchaseOptionId,omitempty"`
	OfferID          string   `json:"offerId,omitempty"`
	Fields           []string `json:"fields,omitempty"`
	From             string   `json:"from,omitempty"`
	To               string   `json:"to,omitempty"`
}

type jsonView struct {
	Package  string         `json:"package"`
	DryRun   bool           `json:"dryRun"`
	Changes  []jsonChange   `json:"changes"`
	Summary  map[string]int `json:"summary"`
	Requires []string       `json:"requires,omitempty"`
}

func (v View) JSON(w io.Writer) error {
	entries := v.Plan.Entries()
	changes := make([]jsonChange, 0, len(entries))
	for _, e := range entries {
		c := jsonChange{Op: v.op(e), Kind: e.Kind, ProductID: e.ProductID, OfferID: e.OfferID, Fields: e.Fields, From: e.From, To: e.To}
		if v.Axis.basePlans {
			c.BasePlanID = e.ParentID
		} else {
			c.PurchaseOptionID = e.ParentID
		}
		changes = append(changes, c)
	}
	_, counts := v.summary()
	return output.WriteJSON(w, jsonView{
		Package:  v.Package,
		DryRun:   v.DryRun,
		Changes:  changes,
		Summary:  counts,
		Requires: v.Requires,
	})
}
