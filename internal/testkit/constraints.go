package testkit

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
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
	// The immutability half of the description needs the release Play
	// stores, which a body rule does not see.
	"TrackRelease.inAppUpdatePriority": {
		sentence: "Can take values in the range [0, 5], with 5 the highest priority.",
		check: func(o map[string]any) string {
			if p, set := present(o, "inAppUpdatePriority"); set {
				return between("inAppUpdatePriority", p, 0, 5)
			}
			return ""
		},
	},
	"TrackConfig.type": {
		sentence: "Currently, the only supported value is closedTesting.",
		check: func(o map[string]any) string {
			if t, set := present(o, "type"); set && t != "CLOSED_TESTING" {
				return fmt.Sprintf("type is %s, not CLOSED_TESTING", quoted(t))
			}
			return ""
		},
	},
	// Play says "approximately": the rule holds the documented figure, so a
	// test that needs a longer reply is one that exercises the refusal.
	"ReviewsReplyRequest.replyText": {
		sentence: "Replies of more than approximately 350 characters will be rejected.",
		check: func(o map[string]any) string {
			if s, _ := o["replyText"].(string); utf8.RuneCountInString(s) > 350 {
				return fmt.Sprintf("replyText is %d characters long, more than 350", utf8.RuneCountInString(s))
			}
			return ""
		},
	},
	"GooglePlayDeveloperReportingV1beta1TimelineSpec.startTime": {
		sentence: `* HOURLY: the 'minutes', 'seconds' and 'nanos' fields must be unset. The time_zone can be left unset (defaults to UTC) or set explicitly to "UTC". Setting any other utc_offset or timezone id will result in a validation error. * DAILY: the 'hours', 'minutes', 'seconds' and 'nanos' fields must be unset.`,
		check: func(o map[string]any) string {
			return aligned("startTime", o)
		},
	},
	"GooglePlayDeveloperReportingV1beta1TimelineSpec.endTime": {
		sentence: "See start_time for restrictions. The timezone of the end point must match the timezone of the start point.",
		// Each aggregation period takes a single zone, so two aligned ends
		// share it: the zone match needs no check of its own.
		check: func(o map[string]any) string {
			return aligned("endTime", o)
		},
	},
	"SubscriptionOffer.phases": {
		sentence: "Must contain at least one and at most two entries.",
		check: func(o map[string]any) string {
			// A patch that leaves phases out keeps the stored ones.
			if v, set := present(o, "phases"); set {
				if phases, _ := v.([]any); len(phases) < 1 || len(phases) > 2 {
					return fmt.Sprintf("phases holds %d entries, not one or two", len(phases))
				}
			}
			return ""
		},
	},
	"SubscriptionListing.benefits": {
		sentence: "Ordered list of at most four benefits.",
		check: func(o map[string]any) string {
			if benefits, _ := o["benefits"].([]any); len(benefits) > 4 {
				return fmt.Sprintf("benefits holds %d entries, more than four", len(benefits))
			}
			return ""
		},
	},
	"OneTimeProductDiscountedOffer.redemptionLimit": redemptionLimit,
	"OneTimeProductGameRewardOffer.redemptionLimit": redemptionLimit,
	"Money.nanos": {
		sentence: "The value must be between -999,999,999 and +999,999,999 inclusive. If `units` is positive, `nanos` must be positive or zero. If `units` is zero, `nanos` can be positive, zero, or negative. If `units` is negative, `nanos` must be negative or zero.",
		check: func(o map[string]any) string {
			v, set := present(o, "nanos")
			if !set {
				return ""
			}
			if why := between("nanos", v, -999_999_999, 999_999_999); why != "" {
				return why
			}
			nanos, _ := number(v)
			units, _ := number(o["units"])
			if units > 0 && nanos < 0 || units < 0 && nanos > 0 {
				return fmt.Sprintf("nanos %v has the opposite sign of units %v", v, o["units"])
			}
			return ""
		},
	},
}

var redemptionLimit = rule{
	sentence: "If unset or set to 0, allows for unlimited offer redemptions. Otherwise must be a number between 1 and 50 inclusive.",
	check: func(o map[string]any) string {
		if v, set := present(o, "redemptionLimit"); set {
			return between("redemptionLimit", v, 0, 50)
		}
		return ""
	},
}

// aligned checks one end of a TimelineSpec against its aggregation period.
// proto3 JSON writes an unset integer as 0 or leaves it out, so only a
// non-zero value breaks "must be unset".
func aligned(end string, spec map[string]any) string {
	dt, _ := spec[end].(map[string]any)
	if dt == nil {
		return ""
	}
	var unset []string
	switch spec["aggregationPeriod"] {
	case "HOURLY":
		unset = []string{"minutes", "seconds", "nanos"}
		if z := zone(dt); z != "" && z != "UTC" {
			return fmt.Sprintf("%s is in time zone %q on an HOURLY timeline, which takes UTC alone", end, z)
		}
	case "DAILY":
		// Every metric set's timelineSpec says America/Los_Angeles is the
		// only DAILY zone.
		unset = []string{"hours", "minutes", "seconds", "nanos"}
		if z := zone(dt); z != "" && z != "America/Los_Angeles" {
			return fmt.Sprintf("%s is in time zone %q on a DAILY timeline, which takes America/Los_Angeles alone", end, z)
		}
	}
	for _, field := range unset {
		if n, _ := number(dt[field]); n != 0 {
			return fmt.Sprintf("%s.%s is %v on a %s timeline, where it must be unset", end, field, dt[field], spec["aggregationPeriod"])
		}
	}
	return ""
}

// zone names the time zone of a google.type.DateTime: its IANA id, its UTC
// offset when set instead, "" when neither is.
func zone(dt map[string]any) string {
	if tz, _ := dt["timeZone"].(map[string]any); tz != nil {
		if id, _ := tz["id"].(string); id != "" {
			return id
		}
	}
	if off, _ := dt["utcOffset"].(string); off != "" && off != "0s" {
		return "UTC" + off
	}
	return ""
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

// number reads a JSON number. proto3 JSON also accepts a number written as a
// string (an int64 always is), so "0.8" counts as 0.8.
func number(v any) (float64, bool) {
	if s, isString := v.(string); isString {
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	f, ok := v.(float64)
	return f, ok
}

// openUnit requires v to be a number strictly between 0 and 1.
func openUnit(name string, v any) string {
	if f, ok := number(v); !ok || !(f > 0 && f < 1) {
		return fmt.Sprintf("%s is %v, not strictly between 0 and 1", name, v)
	}
	return ""
}

// between requires v to be an integer from lo to hi inclusive.
func between(name string, v any, lo, hi float64) string {
	if f, ok := number(v); !ok || f != math.Trunc(f) || f < lo || f > hi {
		return fmt.Sprintf("%s is %v, not an integer from %v to %v", name, v, lo, hi)
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
