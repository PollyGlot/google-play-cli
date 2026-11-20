package transport

import (
	"encoding/base64"
	"testing"
)

func FuzzScopesFromAssertion(f *testing.F) {
	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	jwt := func(payload string) string {
		return b64(`{"alg":"RS256"}`) + "." + b64(payload) + "." + "sig"
	}
	f.Add(jwt(`{"scope":"https://www.googleapis.com/auth/androidpublisher"}`))
	f.Add(jwt(`{"scope":"a b c","iss":"x"}`))
	f.Add(jwt(`{"scope":""}`))
	f.Add(jwt(`{"noscope":true}`))
	f.Add(jwt(`not json`))
	f.Add("only.two")
	f.Add("a.b.c.d")
	f.Add("...")
	f.Add("")
	f.Add(`{"scope":"x"}`)

	f.Fuzz(func(t *testing.T, assertion string) {
		scopes := scopesFromAssertion(assertion)
		for i, s := range scopes {
			if s == "" {
				t.Errorf("scopesFromAssertion(%q) returned an empty scope at index %d", assertion, i)
			}
		}
	})
}
