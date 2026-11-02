package testkit

import (
	"fmt"
	"strconv"
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
	if !ok || f <= 0 || f >= 1 {
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
