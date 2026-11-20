package api

type ResourceKind string

const (
	KindPackage          ResourceKind = "package"
	KindApp              ResourceKind = "app"
	KindDeveloperAccount ResourceKind = "developerAccount"
	KindGamesApplication ResourceKind = "gamesApplication"
	KindAchievement      ResourceKind = "achievement"
	KindLeaderboard      ResourceKind = "leaderboard"
	KindBucket           ResourceKind = "bucket"
)

type Resource struct {
	Kind ResourceKind
	ID   string
}

func PackageResource(pkg string) Resource {
	if pkg == "" {
		return Resource{}
	}
	return Resource{Kind: KindPackage, ID: pkg}
}

func (r Resource) IsZero() bool { return r.ID == "" }

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
