// Package vocab is gplay's offline permission vocabulary for `gplay team`:
// the curated aliases, the frozen role bundles, the raw-`CAN_*` escape hatch,
// and the admin-conferring detection. It is the single source of truth shared
// by `gplay team permissions` (which publishes it) and every `team` write
// command (which resolves --role / --permissions through it). See ADR-0016.
//
// Two facts shape the module (ADR-0016):
//
//   - Aliases are scope-INDEPENDENT. The same alias resolves to the account
//     `_GLOBAL` enum under `team users` (Scope Account) and the bare app-level
//     enum under `team grants` (Scope App); the module appends/strips the
//     `_GLOBAL` suffix by scope. The six account-only capabilities (Play
//     Games, managed Play, connected apps) have no app-level enum and so are
//     not resolvable under Scope App.
//   - Raw enums are scope-LITERAL. Any literal `CAN_*` token is always
//     accepted verbatim (the escape hatch, so a permission Google ships before
//     gplay has an alias is still grantable); gplay is a convenience layer,
//     never a gate. Two guards: a `*_UNSPECIFIED` sentinel is rejected, and
//     the two deprecated values are accepted with a steering warning.
package vocab

import (
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// Scope is the permission family a token resolves into: account-wide
// (`developerAccountPermissions`, `_GLOBAL`-suffixed, on a User) or per-app
// (`appLevelPermissions`, on a Grant).
type Scope int

const (
	// Account is the account-wide scope used by `gplay team users`: aliases
	// resolve to the `_GLOBAL`-suffixed enum.
	Account Scope = iota
	// App is the per-app scope used by `gplay team grants`: aliases resolve to
	// the bare (non-`_GLOBAL`) enum.
	App
)

// String renders the scope as it appears in flags and output ("account" / "app").
func (s Scope) String() string {
	if s == App {
		return "app"
	}
	return "account"
}

// ParseScope maps a --scope flag value to a Scope. An empty value defaults to
// Account (the account-wide family). An unrecognised value is a CLI misuse
// (*exit.UsageError, exit 2).
func ParseScope(v string) (Scope, error) {
	switch strings.TrimSpace(v) {
	case "", "account":
		return Account, nil
	case "app":
		return App, nil
	default:
		return Account, exit.Usagef("unknown --scope %q (valid: account, app)", v)
	}
}

// adminStem is the all-permissions enum stem. Granting CAN_MANAGE_PERMISSIONS
// (app) or CAN_MANAGE_PERMISSIONS_GLOBAL (account) confers full control, so it
// is the one value that trips the --grant-admin write-safety gate (ADR-0017).
const adminStem = "CAN_MANAGE_PERMISSIONS"

// Alias is one curated, scope-independent permission alias (ADR-0016 §1). stem
// is the enum WITHOUT the `_GLOBAL` suffix; the account enum is always
// stem+"_GLOBAL" and the app enum is the bare stem (only when !accountOnly).
type Alias struct {
	// Name is the friendly, scope-independent alias (e.g. "release-production").
	Name string
	// Label is the human-readable description shown by `team permissions`.
	Label string

	stem        string
	accountOnly bool
}

// AccountEnum returns the account-wide (`_GLOBAL`-suffixed) enum for the alias.
func (a Alias) AccountEnum() string { return a.stem + "_GLOBAL" }

// AppEnum returns the per-app enum for the alias and whether one exists; the
// six account-only capabilities have no app-level enum (ok=false).
func (a Alias) AppEnum() (string, bool) {
	if a.accountOnly {
		return "", false
	}
	return a.stem, true
}

// AccountOnly reports whether the alias is an account-wide-only capability
// (Play Games, managed Play, connected apps) with no per-app enum.
func (a Alias) AccountOnly() bool { return a.accountOnly }

// Enum returns the alias's enum under scope and whether it is valid there. An
// account-only alias under Scope App returns ("", false).
func (a Alias) Enum(scope Scope) (string, bool) {
	if scope == App {
		return a.AppEnum()
	}
	return a.AccountEnum(), true
}

// IsAdminConferring reports whether granting this alias confers admin
// (it is the all-permissions alias).
func (a Alias) IsAdminConferring() bool { return a.stem == adminStem }

// Bundle is a frozen role bundle (ADR-0016 §3): a closed, gplay-defined preset
// that expands to a fixed set of alias members. Membership never changes
// silently — only by an explicit, versioned gplay change (a Public-contract
// event).
type Bundle struct {
	// Name is the role name selected with --role (e.g. "release-manager").
	Name string
	// Members are the alias names this bundle expands to, in order.
	Members []string
}

// readBase is the common least-privilege read base every non-admin bundle
// builds on (ADR-0016 §3).
var readBase = []string{"view-app-info", "view-app-quality"}

// aliases is the curated alias registry in display order: the meaningful read
// capabilities first, then the management capabilities, then the sensitive
// money capabilities, then the six account-only capabilities. Declaration
// order is the order `team permissions` prints. The deprecated values
// (CAN_SEE_ALL_APPS, CAN_ACCESS_APP) are deliberately NOT aliases — gplay
// never offers a deprecated value, steering raw users to the modern one
// (ADR-0016 §2 / consequences).
var aliases = []Alias{
	{Name: "view-app-info", stem: "CAN_VIEW_NON_FINANCIAL_DATA", Label: "View app information (read-only)"},
	{Name: "view-app-quality", stem: "CAN_VIEW_APP_QUALITY", Label: "View app quality (Android vitals, crashes & ANRs)"},
	{Name: "reply-reviews", stem: "CAN_REPLY_TO_REVIEWS", Label: "Reply to user reviews"},
	{Name: "release-testing", stem: "CAN_MANAGE_TRACK_APKS", Label: "Release to testing tracks"},
	{Name: "release-production", stem: "CAN_MANAGE_PUBLIC_APKS", Label: "Release to production, exclude devices & use Play App Signing"},
	{Name: "manage-testers", stem: "CAN_MANAGE_TRACK_USERS", Label: "Manage testing tracks & tester lists"},
	{Name: "manage-store-listing", stem: "CAN_MANAGE_PUBLIC_LISTING", Label: "Manage store presence (store listing, pricing & distribution)"},
	{Name: "manage-draft-apps", stem: "CAN_MANAGE_DRAFT_APPS", Label: "Create, edit & delete draft apps"},
	{Name: "manage-app-content", stem: "CAN_MANAGE_APP_CONTENT", Label: "Manage policy & app-content declarations"},
	{Name: "manage-deeplinks", stem: "CAN_MANAGE_DEEPLINKS", Label: "Manage deep links setup"},
	{Name: "view-financial", stem: "CAN_VIEW_FINANCIAL_DATA", Label: "View financial data (sales, payouts, breakdowns)"},
	{Name: "manage-orders", stem: "CAN_MANAGE_ORDERS", Label: "Manage orders & subscriptions"},
	{Name: "manage-permissions", stem: adminStem, Label: "Admin — manage permissions for this scope (confers full control)"},
	// Account-only capabilities: no per-app enum, never inside a bundle.
	{Name: "edit-games", stem: "CAN_EDIT_GAMES", accountOnly: true, Label: "Edit Play Games Services projects (account-wide)"},
	{Name: "publish-games", stem: "CAN_PUBLISH_GAMES", accountOnly: true, Label: "Publish Play Games Services projects (account-wide)"},
	{Name: "create-managed-play-apps", stem: "CAN_CREATE_MANAGED_PLAY_APPS", accountOnly: true, Label: "Create managed Play (private) apps (account-wide)"},
	{Name: "manage-managed-play", stem: "CAN_CHANGE_MANAGED_PLAY_SETTING", accountOnly: true, Label: "Change managed Play settings (account-wide)"},
	{Name: "view-connected-apps", stem: "CAN_VIEW_CONNECTED_APPS", accountOnly: true, Label: "View connected (SDK) apps (account-wide)"},
	{Name: "edit-connected-apps", stem: "CAN_EDIT_CONNECTED_APPS", accountOnly: true, Label: "Edit connected (SDK) apps (account-wide)"},
}

// bundles is the frozen role-bundle registry in display order, least to most
// privilege. Money (view-financial, manage-orders) and account-only
// capabilities are deliberately absent — they are reachable only via explicit
// --permissions or a raw enum, never tucked inside a friendly role (ADR-0016
// §4). admin is exactly the all-permissions alias (ADR-0016 §5).
var bundles = []Bundle{
	{Name: "viewer", Members: readBase},
	{Name: "reviewer", Members: append(append([]string{}, readBase...), "reply-reviews")},
	{Name: "tester-manager", Members: append(append([]string{}, readBase...), "release-testing", "manage-testers")},
	{Name: "release-manager", Members: append(append([]string{}, readBase...), "release-testing", "release-production", "manage-store-listing")},
	{Name: "admin", Members: []string{"manage-permissions"}},
}

// deprecated maps a deprecated raw enum (as a caller might type it) to the
// modern equivalent the steering warning points at (ADR-0016 §2). Both values
// are accepted verbatim — gplay never gates — but warn.
var deprecated = map[string]string{
	"CAN_SEE_ALL_APPS": "CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL", // legacy account-wide
	"CAN_ACCESS_APP":   "CAN_VIEW_NON_FINANCIAL_DATA",        // legacy app-level
}

// aliasByName / bundleByName index the registries for O(1) lookup; built once.
var (
	aliasByName  = make(map[string]Alias, len(aliases))
	bundleByName = make(map[string]Bundle, len(bundles))
)

func init() {
	for _, a := range aliases {
		aliasByName[a.Name] = a
	}
	for _, b := range bundles {
		bundleByName[b.Name] = b
	}
}

// Aliases returns the curated alias registry in display order (a copy).
func Aliases() []Alias { return append([]Alias{}, aliases...) }

// Bundles returns the frozen role-bundle registry in display order (a copy).
func Bundles() []Bundle { return append([]Bundle{}, bundles...) }

// RoleNames returns the frozen role names in display order — used to build
// flag help and the unknown-role error message.
func RoleNames() []string {
	names := make([]string, len(bundles))
	for i, b := range bundles {
		names[i] = b.Name
	}
	return names
}

// Enums resolves a bundle's members to enum values under scope, in member
// order. Every bundle member is a common (non-account-only) alias, so each
// resolves in either scope.
func (b Bundle) Enums(scope Scope) []string {
	out := make([]string, 0, len(b.Members))
	for _, m := range b.Members {
		if e, ok := aliasByName[m].Enum(scope); ok {
			out = append(out, e)
		}
	}
	return out
}

// IsAdminConferring reports whether the bundle confers admin (its resolved set
// includes the all-permissions enum).
func (b Bundle) IsAdminConferring() bool {
	for _, m := range b.Members {
		if aliasByName[m].IsAdminConferring() {
			return true
		}
	}
	return false
}

// BundlesContaining returns the names of the role bundles that include
// aliasName, in bundle display order — the "including-bundles" column of
// `team permissions`.
func BundlesContaining(aliasName string) []string {
	var out []string
	for _, b := range bundles {
		for _, m := range b.Members {
			if m == aliasName {
				out = append(out, b.Name)
				break
			}
		}
	}
	return out
}

// isAdminEnum reports whether e is the all-permissions enum in either scope —
// so admin is detected however it was expressed (alias, role, or a raw enum in
// the "wrong" scope).
func isAdminEnum(e string) bool {
	return e == adminStem || e == adminStem+"_GLOBAL"
}

// IsAdminConferring reports whether a resolved enum set includes the
// all-permissions enum (CAN_MANAGE_PERMISSIONS[_GLOBAL]) — the trigger for the
// --grant-admin gate (ADR-0017 §1).
func IsAdminConferring(enums []string) bool {
	for _, e := range enums {
		if isAdminEnum(e) {
			return true
		}
	}
	return false
}

// ResolvePermissions resolves a list of --permissions tokens (each an alias or
// a raw CAN_* enum) to API enum values under scope, in input order with
// duplicates removed. It returns any steering warnings (a deprecated raw enum
// was used) and a *exit.UsageError (exit 2) — whose message points at
// `gplay team permissions` — for an unknown alias, a `*_UNSPECIFIED` sentinel,
// or an account-only alias used under Scope App.
func ResolvePermissions(scope Scope, tokens []string) (enums []string, warnings []string, err error) {
	seen := make(map[string]bool, len(tokens))
	for _, raw := range tokens {
		tok := strings.TrimSpace(raw)
		if tok == "" {
			continue
		}

		if a, ok := aliasByName[tok]; ok {
			e, ok := a.Enum(scope)
			if !ok {
				return nil, nil, exit.Usagef("permission %q is account-wide only and has no per-app form; grant it with `gplay team users` (account scope), or drop it", tok)
			}
			if !seen[e] {
				seen[e] = true
				enums = append(enums, e)
			}
			continue
		}

		// Not an alias: the raw-enum escape hatch (ADR-0016 §2). A
		// *_UNSPECIFIED sentinel is the only non-grantable value and is
		// rejected; every other CAN_* is accepted verbatim (deprecated ones
		// with a steering warning).
		if strings.HasSuffix(tok, "_UNSPECIFIED") {
			return nil, nil, exit.Usagef("permission %q is a non-grantable sentinel and cannot be set; run `gplay team permissions` for the valid aliases", tok)
		}
		if strings.HasPrefix(tok, "CAN_") {
			if modern, dep := deprecated[tok]; dep {
				warnings = append(warnings, "permission "+tok+" is deprecated; prefer "+modern)
			}
			if !seen[tok] {
				seen[tok] = true
				enums = append(enums, tok)
			}
			continue
		}

		return nil, nil, exit.Usagef("unknown permission %q — run `gplay team permissions` to list valid aliases, or pass a raw CAN_* enum", tok)
	}
	return enums, warnings, nil
}

// ResolveRole expands a frozen role bundle to its enum set under scope. An
// unknown role is a CLI misuse (*exit.UsageError, exit 2) listing the valid
// roles. Role expansion never warns (bundles hold no deprecated values).
func ResolveRole(scope Scope, role string) ([]string, error) {
	b, ok := bundleByName[strings.TrimSpace(role)]
	if !ok {
		return nil, exit.Usagef("unknown role %q (valid: %s) — run `gplay team permissions` to see what each grants", role, strings.Join(RoleNames(), ", "))
	}
	return b.Enums(scope), nil
}

// SortedEnums returns enums sorted lexicographically — a stable form for a
// diff or a deterministic payload preview. The resolver preserves input order;
// callers wanting determinism across differently-ordered inputs sort.
func SortedEnums(enums []string) []string {
	out := append([]string{}, enums...)
	sort.Strings(out)
	return out
}
