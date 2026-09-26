package main

// Tables and ratchet allowlists for TestLeafContract (contract_test.go).
//
// Keys are leaf paths without the root ("releases upload") and, for flags,
// "<leaf path> --<flag>". A global flag is keyed "--<flag>".

// flagVocabulary maps each canonical flag name to its concept: one concept,
// one name. It was seeded from the flags shipped at 1.6.1; where one concept
// had several names, the name used on frozen leaves (or on the most leaves)
// became canonical, and 2.0.0 removed the other names without an alias
// (decision #597, ADR-0048). The groups still undecided sit in flagNameDrift.
//
// A new flag either reuses the canonical name of its concept or adds a new
// concept here, in review, where the collision with an existing one is seen.
var flagVocabulary = map[string]string{
	// Global flags (root persistent).
	"service-account": "service-account JSON, as a path or inline content",
	"account":         "name of a stored Account",
	"verbose":         "log flow steps to stderr",
	"timeout":         "per-request API timeout",
	"retry":           "retry transient failures up to N times",

	// Addressing: which object the command acts on.
	"package":            "Android package name the command acts on (Project pin and env fill it)",
	"store-package":      "package name of the alternative app store (appstore axis)",
	"developer-id":       "Play Console developer account ID",
	"application-id":     "Play Games Services application ID",
	"track":              "release track name",
	"from":               "source track of a promotion",
	"to":                 "destination track of a promotion",
	"version-code":       "versionCode of an uploaded artifact",
	"references-version": "versionCode whose already-uploaded expansion file is referenced",
	"release-name":       "release name, disambiguating releases within a track",
	"review-id":          "review ID",
	"user":               "team member email",
	"product":            "subscription product ID",
	"base-plan":          "subscription base plan ID",
	"organization":       "organization ID a custom app is published to",
	"locale":             "BCP-47 locale of the localized fields written, or locales acted on",
	"allow-locale":       "locale admitted beyond Play's published list",
	"type":               "subtype of the addressed resource (mapping, expansion file, image, achievement type)",
	"format":             "artifact container: apk or bundle",
	"device-tier-config": "device tier config (an ID, or LATEST) a bundle's deliverables are generated with",
	"scope":              "permission scope: account or app",

	// Input and output.
	"output":            "structured output format (output.RegisterFlag)",
	"columns":           "table columns to render",
	"dir":               "local directory tree read or written",
	"file":              "one input document read from a path, or - for stdin",
	"dest":              "destination path for downloaded binary bytes, or - for stdout",
	"batch":             "TSV file of batched inputs, or - for stdin",
	"mapping":           "deobfuscation file uploaded with the artifact",
	"release-notes":     "release notes text",
	"release-notes-dir": "directory of per-locale release notes",
	"bucket":            "Cloud Storage bucket read from",
	"lineage":           "apksigner proof-of-rotation lineage file",
	"upload-cert":       "upload key certificate file",
	"kms-cert":          "certificate of the Cloud KMS signing key",
	"kms-key":           "Cloud KMS signing key resource name",

	// Listing, filtering and time windows.
	"page-size":  "server-side page size",
	"page-token": "server-side page token to resume from",
	"limit":      "client-side cap on the result count",
	"filter":     "AIP-160 filter expression",
	"order-by":   "sort order expression",
	"stars":      "star-rating filter",
	"since":      "start of the time window, as a length back from now",
	"period":     "aggregation period",
	"by":         "dimension to slice a timeline by",
	"dimensions": "dimensions of a raw metric query",
	"metrics":    "metrics of a raw metric query",
	"check":      "check to run (repeatable)",
	"skip-check": "check to skip (repeatable)",
	"method":     "HTTP method filter for the API index",
	"list":       "print the compact catalog",
	"codes":      "print the diagnostic-code catalog",
	"oldest":     "cutoff before which price cohorts migrate",
	"describe":   "describe the metric set instead of querying it",

	// Safety and execution control.
	"dry-run":              "plan and print without writing",
	"confirm":              "consent to an irreversible or production-impacting write",
	"grant-admin":          "consent to granting admin permissions",
	"keep-edit-on-failure": "keep the Edit open on failure for debugging",
	// Edit commit opt-ins (#598): Discovery's edits.commit query parameters.
	"changes-in-review":           "what an Edit commit does with changes already in review: cancel or error",
	"changes-not-sent-for-review": "commit an Edit without sending its changes for review",
	"skip-preflight":              "skip the local check before an upload or write",
	"no-verify":                   "skip the remote access probe",
	"live":                        "also check server-side state",

	// Release state.
	"staged":   "rollout user fraction of a staged release",
	"complete": "force the release status to completed",
	"draft":    "force the release status to draft",

	// Field values written by the command.
	"name":                  "name given to the object created or registered",
	"title":                 "display title",
	"description":           "localized description",
	"contact-email":         "developer contact email",
	"contact-phone":         "developer contact phone",
	"contact-website":       "developer contact website",
	"default-language":      "default language of the app or listing",
	"currency":              "ISO 4217 currency code",
	"price":                 "price amount",
	"price-increase-type":   "subscriber consent mode of a price increase",
	"initial-state":         "initial state of an achievement",
	"point-value":           "achievement point value",
	"steps-to-unlock":       "steps to unlock an incremental achievement",
	"score-min":             "leaderboard minimum score",
	"score-max":             "leaderboard maximum score",
	"score-order":           "leaderboard score order",
	"role":                  "team role",
	"permissions":           "team permission set",
	"group":                 "Google Group email of testers",
	"regions":               "region codes targeted",
	"regions-version":       "regions version pin sent with writes",
	"sdk-levels":            "Android SDK levels targeted",
	"all-users":             "target all users",
	"remote-in-app-update":  "use a remote in-app update as the recovery action",
	"allow-unknown-devices": "accept device selectors Play does not know yet",
	"reply":                 "reply text",
	"reason":                "reason recorded with the action",
	"revoke":                "also revoke the buyer's entitlement",
	"clear":                 "replace the set with an empty set",
	"prune":                 "also delete what is live but absent locally",
	"migrate":               "authorize one-way legacy to v2 promotions",
	"activate":              "mark the new Account active",
	"new-app":               "the app has never published to Open testing or Production",
}

// flagNameDrift lists today's flags that name a concept already in
// flagVocabulary under another name (or reuse a canonical name for another
// concept). 2.0.0 settled every group #597 decided (--staged, --version-code,
// --page-size, --file, --regions, --skip-preflight, --format) with no alias;
// what is left waits on a name nobody has chosen yet.
var flagNameDrift = ratchet{
	name:    "flagNameDrift",
	ceiling: 5,
	entries: []string{
		// Time windows start at --since elsewhere (a length back from now);
		// these take absolute bounds and no end-of-window name has been chosen
		// yet, so both ends of both ranges wait. `reviews history --from/--to`
		// also reuse the promotion's track flags for months.
		"appstore catalog events list --end-time",
		"appstore catalog events list --start-time",
		"reviews history --from",
		"reviews history --month",
		"reviews history --to",
	},
}

// verbVocabulary maps the last token of a leaf path to its category in
// docs/DESIGN.md section 0 (ADR-0019). `status` is deliberately absent: DESIGN
// reserves it for one command, so it is path-keyed in verbPathExceptions.
var verbVocabulary = map[string]string{
	// 1. CRUD grammar.
	"list":   "crud",
	"view":   "crud",
	"create": "crud",
	"add":    "crud",
	"remove": "crud",
	"set":    "crud",

	// 2. Domain verbs listed in DESIGN section 0.
	"upload":        "domain",
	"promote":       "domain",
	"rollout":       "domain",
	"halt":          "domain",
	"resume":        "domain",
	"complete":      "domain",
	"reply":         "domain",
	"pull":          "domain",
	"apply":         "domain",
	"validate":      "domain",
	"download":      "domain",
	"enroll":        "domain",
	"rotate":        "domain",
	"login":         "domain",
	"logout":        "domain",
	"deploy":        "domain",
	"cancel":        "domain",
	"add-targeting": "domain",

	// 2b. Domain verbs shipped before the vocabulary was checked, listed in
	// the DESIGN section 0 table with the gesture each one states.
	"begin":   "domain",
	"commit":  "domain",
	"discard": "domain",
	"refund":  "domain",
	"convert": "domain",
	"migrate": "domain",
	"history": "domain",
	"audit":   "domain",

	// 2c. Admitted with the 2.0.0 verb renames (#597): `appstore submit` sends
	// a hosted app to Google review, irrevocably, which `set` would hide.
	"submit": "domain",
}

// verbPathExceptions are leaves outside the verb grammar for a documented
// reason. They are permanent, unlike verbDrift: the only check is that each
// still names a leaf.
var verbPathExceptions = map[string]string{
	// 3. Reference, diagnostic and scaffold commands (DESIGN section 0).
	"version":          "reference",
	"exit-codes":       "reference",
	"install-skills":   "scaffold",
	"schema":           "reference",
	"init":             "scaffold",
	"apps init":        "scaffold",
	"auth doctor":      "diagnostic",
	"team permissions": "reference (offline catalog)",
	// Session and health, the one `status` of DESIGN section 0.
	"auth status": "status",
	// A second `status`, frozen before the rule was checked: the local Edit pin.
	"edits status": "status (frozen exception)",
	// Vitals hybrid query model (ADR-0027): presets named after the metric set
	// sit over `vitals query`; there is no resource to view, only metrics.
	"vitals query":           "ADR-0027",
	"vitals anomalies":       "ADR-0027",
	"vitals anr":             "ADR-0027",
	"vitals crashes":         "ADR-0027",
	"vitals excessivewakeup": "ADR-0027",
	"vitals lmk":             "ADR-0027",
	"vitals slowrendering":   "ADR-0027",
	"vitals slowstart":       "ADR-0027",
	"vitals stuckbgwakelock": "ADR-0027",
	"vitals errors counts":   "ADR-0027",
	"vitals errors issues":   "ADR-0027",
	"vitals errors reports":  "ADR-0027",
}

// verbDrift lists experimental leaves whose verb contradicts ADR-0019 (`set`
// not update, `remove` not delete, noun before verb). 2.0.0 renamed every one
// of them (#597), so it is empty and only stops a new one from being admitted.
var verbDrift = ratchet{
	name:    "verbDrift",
	ceiling: 0,
	entries: []string{},
}

// leavesWithoutOutputByDesign are the leaves docs/DESIGN.md section 7 exempts
// from --output (no structured result, or a binary payload). Permanent.
var leavesWithoutOutputByDesign = map[string]string{
	"auth login":                  "DESIGN section 7: free-form human text, no structured result",
	"auth logout":                 "DESIGN section 7: free-form human text, no structured result",
	"releases generated download": "DESIGN section 7: raw binary bytes to --dest (ADR-0034)",
	"exit-codes":                  "help topic (gplay help exit-codes), not a runnable command",
}

// leavesWithoutOutput are today's leaves with no --output that DESIGN section 7
// does not document. Each either gains --output or gets documented there and
// moves to leavesWithoutOutputByDesign (COH-13, #597).
var leavesWithoutOutput = ratchet{
	name:    "leavesWithoutOutput",
	ceiling: 6,
	entries: []string{
		"apps add",
		"apps init",
		"apps remove",
		"init",
		"install-skills",
		"version",
	},
}

// leavesWithoutLong are today's leaves whose help has no Long of their own.
var leavesWithoutLong = ratchet{
	name:    "leavesWithoutLong",
	ceiling: 4,
	entries: []string{
		"auth list",
		"auth logout",
		"auth status",
		"version",
	},
}
