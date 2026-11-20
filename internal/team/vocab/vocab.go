package vocab

import (
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

type Scope int

const (
	Account Scope = iota
	App
)

func (s Scope) String() string {
	if s == App {
		return "app"
	}
	return "account"
}

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

const adminStem = "CAN_MANAGE_PERMISSIONS"

type Alias struct {
	Name  string
	Label string

	stem        string
	accountOnly bool
	retired     bool
}

func (a Alias) AccountEnum() string { return a.stem + "_GLOBAL" }

func (a Alias) AppEnum() (string, bool) {
	if a.accountOnly {
		return "", false
	}
	return a.stem, true
}

func (a Alias) AccountOnly() bool { return a.accountOnly }

func (a Alias) Enum(scope Scope) (string, bool) {
	if scope == App {
		return a.AppEnum()
	}
	return a.AccountEnum(), true
}

func (a Alias) IsAdminConferring() bool { return a.stem == adminStem }

func (a Alias) Deprecated() bool { return a.retired }

type Bundle struct {
	Name    string
	Members []string
}

var readBase = []string{"view-app-info", "view-app-quality"}

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
	{Name: "manage-permissions", stem: adminStem, Label: "Admin: manage permissions for this scope (confers full control)"},
	{Name: "edit-games", stem: "CAN_EDIT_GAMES", accountOnly: true, Label: "Edit Play Games Services projects (account-wide)"},
	{Name: "publish-games", stem: "CAN_PUBLISH_GAMES", accountOnly: true, Label: "Publish Play Games Services projects (account-wide)"},
	{Name: "create-managed-play-apps", stem: "CAN_CREATE_MANAGED_PLAY_APPS", accountOnly: true, Label: "Create managed Play (private) apps (account-wide)"},
	{Name: "manage-managed-play", stem: "CAN_CHANGE_MANAGED_PLAY_SETTING", accountOnly: true, retired: true, Label: "Change managed Play settings (account-wide)"},
	{Name: "view-connected-apps", stem: "CAN_VIEW_CONNECTED_APPS", accountOnly: true, Label: "View connected (SDK) apps (account-wide)"},
	{Name: "edit-connected-apps", stem: "CAN_EDIT_CONNECTED_APPS", accountOnly: true, Label: "Edit connected (SDK) apps (account-wide)"},
}

var bundles = []Bundle{
	{Name: "viewer", Members: readBase},
	{Name: "reviewer", Members: append(append([]string{}, readBase...), "reply-reviews")},
	{Name: "tester-manager", Members: append(append([]string{}, readBase...), "release-testing", "manage-testers")},
	{Name: "release-manager", Members: append(append([]string{}, readBase...), "release-testing", "release-production", "manage-store-listing")},
	{Name: "admin", Members: []string{"manage-permissions"}},
}

var deprecated = map[string]string{
	"CAN_SEE_ALL_APPS": "CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL",
	"CAN_ACCESS_APP":   "CAN_VIEW_NON_FINANCIAL_DATA",
}

func deprecationWarning(token, enum string) string {
	subject := token
	if token != enum {
		subject = token + " (" + enum + ")"
	}
	if modern := deprecated[enum]; modern != "" {
		return "permission " + subject + " is deprecated; prefer " + modern
	}
	return "permission " + subject + " is deprecated, no longer supported by Google"
}

var (
	aliasByName  = make(map[string]Alias, len(aliases))
	bundleByName = make(map[string]Bundle, len(bundles))
)

func init() {
	for _, a := range aliases {
		aliasByName[a.Name] = a
		if a.retired {
			deprecated[a.AccountEnum()] = ""
			if e, ok := a.AppEnum(); ok {
				deprecated[e] = ""
			}
		}
	}
	for _, b := range bundles {
		bundleByName[b.Name] = b
	}
}

func Aliases() []Alias { return append([]Alias{}, aliases...) }

func Bundles() []Bundle { return append([]Bundle{}, bundles...) }

func RoleNames() []string {
	names := make([]string, len(bundles))
	for i, b := range bundles {
		names[i] = b.Name
	}
	return names
}

func (b Bundle) Enums(scope Scope) []string {
	out := make([]string, 0, len(b.Members))
	for _, m := range b.Members {
		if e, ok := aliasByName[m].Enum(scope); ok {
			out = append(out, e)
		}
	}
	return out
}

func (b Bundle) IsAdminConferring() bool {
	for _, m := range b.Members {
		if aliasByName[m].IsAdminConferring() {
			return true
		}
	}
	return false
}

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

func isAdminEnum(e string) bool {
	return e == adminStem || e == adminStem+"_GLOBAL"
}

func IsAdminConferring(enums []string) bool {
	for _, e := range enums {
		if isAdminEnum(e) {
			return true
		}
	}
	return false
}

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
			if _, dep := deprecated[e]; dep {
				warnings = append(warnings, deprecationWarning(tok, e))
			}
			if !seen[e] {
				seen[e] = true
				enums = append(enums, e)
			}
			continue
		}

		if strings.HasSuffix(tok, "_UNSPECIFIED") {
			return nil, nil, exit.Usagef("permission %q is a non-grantable sentinel and cannot be set; run `gplay team permissions` for the valid aliases", tok)
		}
		if strings.HasPrefix(tok, "CAN_") {
			if _, dep := deprecated[tok]; dep {
				warnings = append(warnings, deprecationWarning(tok, tok))
			}
			if !seen[tok] {
				seen[tok] = true
				enums = append(enums, tok)
			}
			continue
		}

		return nil, nil, exit.Usagef("unknown permission %q: run `gplay team permissions` to list valid aliases, or pass a raw CAN_* enum", tok)
	}
	return enums, warnings, nil
}

func ResolveRole(scope Scope, role string) ([]string, error) {
	b, ok := bundleByName[strings.TrimSpace(role)]
	if !ok {
		return nil, exit.Usagef("unknown role %q (valid: %s): run `gplay team permissions` to see what each grants", role, strings.Join(RoleNames(), ", "))
	}
	return b.Enums(scope), nil
}
