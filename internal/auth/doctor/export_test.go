package doctor

// ExportCheckScopeRequiring exposes the internal scope-pinning Check
// constructor so tests can simulate the "missing scope" failure path
// without mutating the AndroidPublisherScope constant in the token
// package. End users always go through CheckScope.
func ExportCheckScopeRequiring(scope string) Check {
	return checkScopeRequiring(scope)
}
