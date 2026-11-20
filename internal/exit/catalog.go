package exit

type Doc struct {
	Code      int
	Meaning   string
	RetrySafe string
}

func Catalog() []Doc {
	return []Doc{
		{0, "Success", "n/a"},
		{1, "Generic error (fallback when nothing more specific fits)", "no"},
		{2, "CLI misuse (unknown flag, bad value, repeated single-value flag, wrong number of positional args)", "no"},
		{3, "Safety flag required: well-formed, but a named --confirm/--grant-admin is missing; the message names it", "re-run with the named flag"},
		{4, "Denied by environment policy (GPLAY_READONLY): a mutating command was refused", "no; not resolvable by a flag, change the environment"},
		{10, "Authentication failure (SA invalid, token refused, scope missing, no developer-id)", "no"},
		{11, "Authorization (403: SA not invited on the app/account)", "no"},
		{20, "Client-side validation (malformed AAB, unknown locale, ...)", "no"},
		{30, "API 4xx other than auth/perms (not found, conflict, gone, ...)", "no"},
		{40, "API 5xx (upstream temporarily unhealthy)", "yes"},
		{50, "Network (timeout, DNS, refused)", "yes"},
		{60, "State conflict (open edit, rate-limited, ambiguous target, ...)", "sometimes"},
		{70, "Findings present (a read-only check command completed and reported drift; NOT an error)", "no"},
	}
}
