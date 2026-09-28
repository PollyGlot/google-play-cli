package api

// ResourceKind names the addressing axis a call targets. It travels in the
// JSON error envelope as `resource.kind` (ADR-0023, #599), so the values are
// part of the Public contract: append-only, never renamed or repurposed.
type ResourceKind string

// The kinds gplay addresses today. Package is the default axis
// (`applications/{packageName}/...`); every other kind exists because a module
// addresses something that is not an Android package, and labelling that id a
// package misled every consumer branching on the envelope (ARCH-06).
const (
	// KindPackage is an Android package name, e.g. com.example.app.
	KindPackage ResourceKind = "package"
	// KindApp is a numeric Play Console app ID, which Play App Signing accepts
	// in place of the package name.
	KindApp ResourceKind = "app"
	// KindDeveloperAccount is a Play Console developer account ID (team
	// members and grants, custom apps).
	KindDeveloperAccount ResourceKind = "developerAccount"
	// KindGamesApplication is a Play Games Services application ID.
	KindGamesApplication ResourceKind = "gamesApplication"
	// KindAchievement is a Play Games Services achievement ID.
	KindAchievement ResourceKind = "achievement"
	// KindLeaderboard is a Play Games Services leaderboard ID.
	KindLeaderboard ResourceKind = "leaderboard"
	// KindBucket is a Cloud Storage bucket (the Play Console report exports).
	KindBucket ResourceKind = "bucket"
)

// Resource is what a failed call addressed: the kind of identifier and its
// value. The zero value means "no target known" (a local failure, or a call
// with no addressing key such as an account-wide search).
type Resource struct {
	Kind ResourceKind
	ID   string
}

// PackageResource is the Resource of a package-axis call, for the helpers that
// take a Resource because some of their callers address another axis.
func PackageResource(pkg string) Resource {
	if pkg == "" {
		return Resource{}
	}
	return Resource{Kind: KindPackage, ID: pkg}
}

// IsZero reports whether r names no target.
func (r Resource) IsZero() bool { return r.ID == "" }

// Target returns the resource e addressed. A package-axis error sets only
// Package, the historical field every package module fills, so it is promoted
// here to a KindPackage resource; every other axis sets Resource explicitly.
// Resource wins when both are set, since it is the more specific statement.
func (e *Error) Target() Resource {
	if e == nil {
		return Resource{}
	}
	if !e.Resource.IsZero() {
		return e.Resource
	}
	if e.Package != "" {
		return Resource{Kind: KindPackage, ID: e.Package}
	}
	return Resource{}
}
