package exit

// Doc is one row of the exit-code taxonomy (docs/DESIGN.md §9), surfaced by
// `gplay help exit-codes`.
type Doc struct {
	Code      int
	Meaning   string
	RetrySafe string // "no" | "yes" | a short qualifier
}

// Catalog returns the documented exit-code taxonomy in order — the single
// source of truth shared by `gplay help exit-codes` and this package's
// doc comment. Keep it in lockstep with docs/DESIGN.md §9.
func Catalog() []Doc {
	return []Doc{
		{0, "Success", "—"},
		{1, "Generic error (fallback when nothing more specific fits)", "no"},
		{2, "CLI misuse (unknown flag, bad value, wrong number of positional args)", "no"},
		{3, "Safety flag required — well-formed, but a named --confirm/--grant-admin is missing; the message names it", "re-run with the named flag"},
		{4, "Denied by environment policy (GPLAY_READONLY) — a mutating command was refused; NOT resolvable by adding a flag", "no — change the environment"},
		{10, "Authentication failure (SA invalid, token refused, scope missing, no developer-id)", "no"},
		{11, "Authorization (403 — SA not invited on the app/account)", "no"},
		{20, "Client-side validation (malformed AAB, unknown locale, ...)", "no"},
		{30, "API 4xx other than auth/perms (not found, conflict, gone, ...)", "no"},
		{40, "API 5xx (upstream temporarily unhealthy)", "yes"},
		{50, "Network (timeout, DNS, refused)", "yes"},
		{60, "State conflict (open edit, rate-limited, ambiguous target, ...)", "sometimes"},
	}
}
