package testkit

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

var constraintWording = regexp.MustCompile(`(?i)\bonly\b|\bmust\b|\bimmutable\b|\bcan ?not\b|\bat most\b|\bup to\b|\bmaximum\b|\bno more than\b|\bat least\b|\brejected\b|0 <|strictly (smaller|larger|greater|less)|required (when|if)|not be set|mutually exclusive|exactly one of`)

var fieldBehavior = regexp.MustCompile(`^((Required|Optional|Immutable)\.\s*)+`)

func statesConstraint(description string) bool {
	if strings.HasPrefix(description, "Output only.") {
		return false
	}
	return constraintWording.MatchString(fieldBehavior.ReplaceAllString(description, ""))
}

const (
	userInput = "the value is the user's own input (a JSON or certificate file, a flag), forwarded verbatim: gplay builds none, so Play's refusal names the user's input"
	stored    = "the constraint involves what Play already stores (the resource before the call, its siblings, the app), which a body rule does not see"
	effect    = "states when the field takes effect or what it does, not that Play refuses the request"
	response  = "describes the response or a read-only part of the resource, not the request"
	server    = "rests on Play's server-side data (minimum prices, region currencies), absent from Discovery"
	account   = "constrains the developer account or the app's history, not the body"
	civil     = "vitals fills it from a Go time.Time (toDateTime in commands/vitals/vitalscmd/vitalscmd.go), which cannot hold an out-of-range field; the TimelineSpec rules check its alignment"
)

var unenforced = func() map[string]struct{ sentence, why string } {
	u := map[string]struct{ sentence, why string }{
		"ExpansionFile.referencesVersion": {"The file_size field will not be set.", "describes the response: Play fills fileSize, and gplay's request carries referencesVersion alone"},

		"AchievementConfiguration.published":              {"The read-only published data of the achievement.", response},
		"AchievementConfiguration.stepsToUnlock":          {"Only applicable to incremental achievements.", effect},
		"LeaderboardConfiguration.published":              {"The read-only published data of the leaderboard.", response},
		"GamesNumberFormatConfiguration.currencyCode":     {"Only used for CURRENCY format type.", effect},
		"GamesNumberFormatConfiguration.numDecimalPlaces": {"Only used for NUMERIC format type.", effect},

		"AppStoreAppActiveApkSet.alreadyPublishedOnPlay": {"When this is set, version_code must be provided.", userInput},
		"Date.day":                        {"Must be from 1 to 31 and valid for the year and month", userInput},
		"Date.month":                      {"Must be from 1 to 12, or 0 to specify a year without a month and day.", userInput},
		"Date.year":                       {"Must be from 1 to 9999, or 0 to specify a date without a year.", userInput},
		"PolicyKeyedGroupResponse.groups": {"Each KeyedGroup.key must be unique within this list.", userInput},

		"CloudKmsKey.cryptoKeyVersionResource": {"The Google Play service account must be granted Decrypt and Sign permissions on this resource.", account},
		"CloudKmsKeyAndCert.pemCertificate":    {"The bytes must contain the certificate in PEM format.", userInput},
		"EnrollAppRequest.enrollNewApp":        {"The app must not have published to Open testing or Production tracks.", account},
		"CustomApp.organizations":              {"If the request contains any organizations, then the app will be restricted to only these organizations.", effect},

		"DeviceSelector.systemOnChips": {"Only works for Android S+ devices.", effect},
		"DeviceTier.deviceGroupNames":  {"These groups must be defined explicitly under device_groups in this configuration.", userInput},
		"DeviceTier.level":             {"tier levels in a tier set must be unique.", userInput},

		"ExternallyHostedApk.externallyHostedUrl": {"This must be an https URL.", userInput},
		"TrackRelease.versionCodes":               {"Must include version codes to retain from previous releases.", stored},
		"User.expirationTime":                     {"When setting this value, it must always be in the future.", "gplay never sets it: team users create sends the email and the permissions alone (CreateUser in internal/play/team/team.go)"},

		"AutoRenewingBasePlanType.accountHoldDuration":                           {"Acceptable values must be in days and between P0D and P60D.", userInput},
		"AutoRenewingBasePlanType.gracePeriodDuration":                           {"Acceptable values must be in days and between P0D and the lesser of 30D and base plan billing period.", userInput},
		"AutoRenewingBasePlanType.billingPeriodDuration":                         {"The duration is immutable after the base plan is created.", stored},
		"AutoRenewingBasePlanType.legacyCompatible":                              {"Only one renewing base plan can be marked as legacy compatible for a given subscription.", stored},
		"AutoRenewingBasePlanType.legacyCompatibleSubscriptionOfferId":           {"Only one subscription offer can be marked as legacy compatible for a given renewing base plan.", stored},
		"InstallmentsBasePlanType.accountHoldDuration":                           {"Acceptable values must be in days and between P0D and P60D.", userInput},
		"InstallmentsBasePlanType.gracePeriodDuration":                           {"Acceptable values must be in days and between P0D and the lesser of 30D and base plan billing period.", userInput},
		"InstallmentsBasePlanType.billingPeriodDuration":                         {"The duration is immutable after the base plan is created.", stored},
		"InstallmentsBasePlanType.committedPaymentsCount":                        {"It is immutable after the base plan is created.", stored},
		"InstallmentsBasePlanType.renewalType":                                   {"The renewal type is immutable after the base plan is created.", stored},
		"PrepaidBasePlanType.billingPeriodDuration":                              {"The duration is immutable after the base plan is created.", stored},
		"BasePlan.basePlanId":                                                    {"Must be unique within the subscription, and conform with RFC-1034.", userInput},
		"MigrateBasePlanPricesRequest.packageName":                               {"Must be equal to the package_name field on the Subscription resource.", "gplay fills it from the argument that names the URL path (commands/subscriptions/prices/migrate/migrate.go)"},
		"MigrateBasePlanPricesRequest.productId":                                 {"Must be equal to the product_id field on the Subscription resource.", "gplay fills it from the argument that names the URL path (commands/subscriptions/prices/migrate/migrate.go)"},
		"Subscription.listings":                                                  {"Must contain at least an entry for the default language of the parent app.", stored},
		"Subscription.productId":                                                 {"Product IDs must be composed of lower-case letters (a-z), numbers (0-9), underscores (_) and dots (.).", userInput},
		"SubscriptionOffer.offerId":                                              {"Must be unique within the base plan.", stored},
		"SubscriptionOffer.regionalConfigs":                                      {"Must contain at least one entry.", userInput},
		"SubscriptionOfferPhase.regionalConfigs":                                 {"This list must contain exactly one entry for each region for which the subscription offer has a regional config.", userInput},
		"AcquisitionTargetingRule.scope":                                         {`Only allows "this subscription" and "any subscription in app".`, userInput},
		"UpgradeTargetingRule.scope":                                             {`Only allows "this subscription" and "specific subscription in app".`, userInput},
		"UpgradeTargetingRule.billingPeriodDuration":                             {"that a user must be currently subscribed to to be eligible for this rule.", effect},
		"UpgradeTargetingRule.oncePerUser":                                       {"Limit this offer to only once per user.", effect},
		"RetentionTargetingRule.scope":                                           {`Only allows "this subscription".`, userInput},
		"RetentionTargetingRule.minDuration":                                     {"The minimum duration a user must have been a paid subscriber to be eligible", effect},
		"RetentionTargetingRule.minOfferCooldownDuration":                        {"The minimum duration that must pass after the previous retention offer's discount period ends before the user is eligible again.", effect},
		"TargetingRuleScope.specificSubscriptionInApp":                           {"Must be a subscription within the same parent app.", stored},
		"OtherRegionsSubscriptionOfferPhaseConfig.otherRegionsPrices":            {"The price must not be smaller than the minimum price allowed for any new locations Play may launch in.", server},
		"RegionalSubscriptionOfferPhaseConfig.price":                             {"The price must not be smaller than the minimum price allowed for this region.", server},
		"OneTimeProductPurchaseOptionRegionalPricingAndAvailabilityConfig.price": {"Must be set in the currency that is linked to the specified region.", server},

		"OfferTag.tag":                {"Must conform with RFC-1034.", userInput},
		"OneTimeProduct.listings":     {"Must not have duplicate entries with the same language_code.", userInput},
		"OneTimeProduct.productId":    {"Product IDs must start with a number or lowercase letter", userInput},
		"OneTimeProductOffer.offerId": {"Must be unique within the purchase option.", stored},
		"OneTimeProductOffer.regionalPricingAndAvailabilityConfigs": {"Must not have duplicate entries with the same region_code.", userInput},
		"OneTimeProductPurchaseOption.purchaseOptionId":             {"Must be unique within the one-time product.", stored},
		"RegionalTaxConfig.eligibleForStreamingServiceTaxRate":      {"You must tell us if your app contains streaming products", userInput},

		"GoogleTypeDateTime.year":      {"Must be from 1 to 9999, or 0 if specifying a datetime without a year.", civil},
		"GoogleTypeDateTime.month":     {"Must be from 1 to 12, or 0 if specifying a datetime without a month.", civil},
		"GoogleTypeDateTime.day":       {"Must be from 1 to 31 and valid for the year and month", civil},
		"GoogleTypeDateTime.minutes":   {"Must be from 0 to 59, defaults to 0.", civil},
		"GoogleTypeDateTime.seconds":   {"Must normally be from 0 to 59, defaults to 0.", civil},
		"GoogleTypeDateTime.nanos":     {"Must be from 0 to 999,999,999, defaults to 0.", civil},
		"GoogleTypeDateTime.utcOffset": {"Must be whole seconds, between -18 hours and +18 hours.", "vitals never sets it, and the TimelineSpec rules refuse any zone but the one each aggregation period takes"},

		"OneTimeProductListing.title":                      {"The maximum length is 55 characters.", userInput},
		"OneTimeProductListing.description":                {"The maximum length is 200 characters.", userInput},
		"SubscriptionListing.description":                  {"Maximum length - 200 characters.", userInput},
		"OneTimeProduct.offerTags":                         {"List of up to 20 custom tags specified for this one-time product", userInput},
		"OneTimeProductPurchaseOption.offerTags":           {"List of up to 20 custom tags specified for this purchase option", userInput},
		"OneTimeProductOffer.offerTags":                    {"List of up to 20 custom tags specified for this offer", userInput},
		"BasePlan.offerTags":                               {"List of up to 20 custom tags specified for this base plan", userInput},
		"SubscriptionOffer.offerTags":                      {"List of up to 20 custom tags specified for this offer", userInput},
		"OneTimeProductBuyPurchaseOption.legacyCompatible": {"Up to one \"buy\" purchase option can be marked as backwards compatible.", userInput},
		"UsesPermission.maxSdkVersion":                     {"the maximum SDK version for which the permission is required.", "names a bound the field carries, not a limit Play puts on the request"},
		"ExternallyHostedApk.maximumSdk":                   {"The maximum SDK supported by this APK (optional).", "names a bound the field carries, not a limit Play puts on the request"},
		"DeviceRam.maxBytes":                               {"Maximum RAM in bytes (bound excluded).", "names a bound the field carries, not a limit Play puts on the request"},
		"LeaderboardConfiguration.scoreMax":                {"Maximum score that can be posted to this leaderboard.", "names a bound the field carries, not a limit Play puts on the request"},
		"SafetyLabelsUpdateRequest.safetyLabels":           {"To download an up to date template", "the wording is about the template being current, not a limit"},
	}
	osBeta := map[string]bool{"AnrRate": true, "CrashRate": true, "LmkRate": true}
	for _, set := range []string{"AnonRssAndSwapMemoryUsage", "AnrRate", "BitmapMemoryUsage", "CrashRate", "ErrorCount", "ExcessiveWakeupRate", "LmkRate", "SlowRenderingRate", "SlowStartRate", "StuckBackgroundWakelockRate"} {
		req := "GooglePlayDeveloperReportingV1beta1Query" + set + "MetricSetRequest."
		u[req+"metrics"] = struct{ sentence, why string }{"Care must be taken not to aggregate this count further", response}
		u[req+"pageSize"] = struct{ sentence, why string }{"If unspecified, at most 1000 rows will be returned.", response}
		u[req+"pageToken"] = struct{ sentence, why string }{"When paginating, all other parameters provided to the request must match the call that provided the page token.", "vitals.Query resends the body it was given with pageToken alone changed (internal/play/vitals/vitals.go)"}
		u[req+"timelineSpec"] = struct{ sentence, why string }{"only supported timezone is `America/Los_Angeles`.", "checked through the GooglePlayDeveloperReportingV1beta1TimelineSpec rules, which refuse any DAILY zone but America/Los_Angeles"}
		switch {
		case set == "ErrorCount":
		case osBeta[set]:
			u[req+"userCohort"] = struct{ sentence, why string }{"To select data from beta android versions only", effect}
		default:
			u[req+"userCohort"] = struct{ sentence, why string }{"The only supported value is `OS_PUBLIC`.", "vitals.SupportedCohorts reads the sentence and Execute refuses any other --user-cohort offline (commands/vitals/vitalscmd/vitalscmd.go)"}
		}
	}
	return u
}()

func embeddedIndex(t *testing.T) schemaindex.Index {
	t.Helper()
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("embedded Schema index: %v", err)
	}
	return idx
}

func description(t *testing.T, idx schemaindex.Index, key string) string {
	t.Helper()
	schema, prop, _ := strings.Cut(key, ".")
	p, ok := idx.Schemas[schema].Properties[prop]
	if !ok {
		t.Fatalf("%s: no such property in the Schema index", key)
	}
	return p.Description
}

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

func TestRules_inventoryDetectsTheConstraintsItOnceMissed(t *testing.T) {
	idx := embeddedIndex(t)
	for _, key := range []string{"TrackRelease.countryTargeting", "TrackConfig.track", "TrackRelease.inAppUpdatePriority", "ReviewsReplyRequest.replyText"} {
		if d := description(t, idx, key); !statesConstraint(d) {
			t.Errorf("%s: the inventory does not see the constraint in:\n%s", key, d)
		}
	}
}

func TestRules_inventoryCoversEveryDocumentedConstraint(t *testing.T) {
	idx := embeddedIndex(t)
	via := written(idx)
	var notWritten []string
	for schema, s := range idx.Schemas {
		for prop, p := range s.Properties {
			key := schema + "." + prop
			if !statesConstraint(p.Description) {
				continue
			}
			_, ruled := rules[key]
			_, excluded := unenforced[key]
			switch id, sent := via[schema]; {
			case ruled && excluded:
				t.Errorf("%s: both in the table and in unenforced; drop the exclusion", key)
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
		if !statesConstraint(description(t, idx, key)) {
			t.Errorf("unenforced %s: the inventory no longer sees a constraint there; drop the entry", key)
		}
	}
}
