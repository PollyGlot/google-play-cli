package testkit

import (
	"fmt"
	"strconv"
	"strings"
)

// rule is one constraint Play documents in prose on a request property and
// refuses at runtime, which a schema alone does not express.
type rule struct {
	// sentence is the Discovery description wording check encodes, verbatim.
	// The anchor test fails when Google rewords it, so a snapshot refresh
	// forces a review of check.
	sentence string
	// check returns why obj, an object of the rule's schema, breaks the rule,
	// or "" when it does not.
	check func(obj map[string]any) string
}

// rules is the request-constraint table, keyed by `Schema.property` as
// Discovery names them. The Fake and the Wire check every request body
// against it, so a test that pins a body Play refuses fails (#731, #735).
// constraints_internal_test.go holds the inventory: every Discovery property
// whose description states a constraint is either here or listed there with
// the reason it is not.
var rules = map[string]rule{
	"TrackRelease.userFraction": {
		sentence: `0 < fraction < 1. Can only be set when status is "inProgress" or "halted".`,
		check: func(o map[string]any) string {
			f, set := present(o, "userFraction")
			if !set {
				return ""
			}
			if s := o["status"]; s != "inProgress" && s != "halted" {
				return fmt.Sprintf("userFraction %v set on a release whose status is %s, not inProgress or halted", f, quoted(s))
			}
			return openUnit("userFraction", f)
		},
	},
	// Discovery names inProgress alone, yet halted counts too: a halted
	// release is a paused staged rollout that keeps its targeting, and
	// halt/resume must round-trip it (#779). The production-track half of the
	// sentence is not checked: the rule sees the release, not its track, and
	// gplay only ever carries countryTargeting over from tracks.get.
	"TrackRelease.countryTargeting": {
		sentence: "Note this is only allowed to be set for inProgress releases in the production track.",
		check: func(o map[string]any) string {
			if _, set := present(o, "countryTargeting"); !set {
				return ""
			}
			if s := o["status"]; s != "inProgress" && s != "halted" {
				return fmt.Sprintf("countryTargeting set on a release whose status is %s, not inProgress or halted", quoted(s))
			}
			return ""
		},
	},
	"OneTimeProductOfferRegionalPricingAndAvailabilityConfig.relativeDiscount": relativeDiscount,
	"OtherRegionsSubscriptionOfferPhaseConfig.relativeDiscount":                relativeDiscount,
	"RegionalSubscriptionOfferPhaseConfig.relativeDiscount":                    relativeDiscount,
	"RegionalBasePlanConfig.price": {
		sentence: `Must be set if the base plan is available to new subscribers.`,
		check: func(o map[string]any) string {
			if _, set := present(o, "price"); !set && o["newSubscriberAvailability"] == true {
				return "newSubscriberAvailability is true but price is not set"
			}
			return ""
		},
	},
	"TrackConfig.track": {
		sentence: "This prefix must match the value of the `form_factor` field, if it is not a default track.",
		check: func(o map[string]any) string {
			track, _ := o["track"].(string)
			ff, _ := o["formFactor"].(string)
			for prefixed, prefix := range map[string]string{"WEAR": "wear:", "AUTOMOTIVE": "automotive:"} {
				switch has := strings.HasPrefix(track, prefix); {
				case ff == prefixed && !has:
					return fmt.Sprintf("track %q lacks the %q prefix its formFactor %s requires", track, prefix, ff)
				case ff != prefixed && has:
					return fmt.Sprintf("track %q carries the %q prefix but formFactor is %s", track, prefix, quoted(o["formFactor"]))
				}
			}
			return ""
		},
	},
	"OneTimeProductTaxAndComplianceSettings.regionalProductAgeRatingInfos": usOnlyAgeRating,
	"SubscriptionTaxAndComplianceSettings.regionalProductAgeRatingInfos":   usOnlyAgeRating,
}

var relativeDiscount = rule{
	sentence: "specified as a fraction strictly larger than `0` and strictly smaller than `1`.",
	check: func(o map[string]any) string {
		if d, set := present(o, "relativeDiscount"); set {
			return openUnit("relativeDiscount", d)
		}
		return ""
	},
}

var usOnlyAgeRating = rule{
	sentence: "Currently this field is only supported for region code `US`.",
	check: func(o map[string]any) string {
		infos, _ := o["regionalProductAgeRatingInfos"].([]any)
		for i, info := range infos {
			m, _ := info.(map[string]any)
			if code := m["regionCode"]; code != "US" {
				return fmt.Sprintf("regionalProductAgeRatingInfos[%d].regionCode is %s, not US", i, quoted(code))
			}
		}
		return ""
	},
}

// present reads key from o. A JSON null counts as absent: proto3 JSON maps it
// to the field's default, which is what an unset field is.
func present(o map[string]any, key string) (any, bool) {
	v, ok := o[key]
	return v, ok && v != nil
}

// openUnit requires v to be a number strictly between 0 and 1. proto3 JSON
// also accepts a double written as a string, so "0.8" counts as 0.8.
func openUnit(name string, v any) string {
	f, ok := v.(float64)
	if s, isString := v.(string); isString {
		var err error
		f, err = strconv.ParseFloat(s, 64)
		ok = err == nil
	}
	if !ok || !(f > 0 && f < 1) {
		return fmt.Sprintf("%s is %v, not strictly between 0 and 1", name, v)
	}
	return ""
}

// quoted renders a JSON value for a violation message, telling a missing
// value apart from an empty string.
func quoted(v any) string {
	if v == nil {
		return "unset"
	}
	return fmt.Sprintf("%q", fmt.Sprint(v))
}
