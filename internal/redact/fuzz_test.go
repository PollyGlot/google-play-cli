package redact

import (
	"strings"
	"testing"
)

func FuzzRedactString(f *testing.F) {
	for _, seed := range [][2]string{
		{"", ""},
		{"could not read credential: bad key: ", "\n"},
		{`json: cannot unmarshal: "`, `\n"`},
		{`{"type":"service_account","private_key":"`, `","private_key_id":"abc123def456"}`},
		{"GET /v3/applications HTTP/1.1\r\n", "\r\n"},
		{`map[Authorization:["`, `"] Accept:["application/json"]]`},
		{"exec: npx --client_secret=", " failed"},
		{"config rejected: password: ", ""},
		{"password=", "]"},
		{"proxy refused: Basic ", "="},
		{"-----BEGIN PRIVATE KEY-----", "-----END PRIVATE KEY-----"},
		{"Authorization: Bearer abc", "x"},
		{"assertion rejected: eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.", ".sig"},
	} {
		f.Add(seed[0], seed[1])
	}

	const pemBody = "MIIEvQIBADANBgkqhkiG9w0BFUZZPEMBODY"
	const pem = "-----BEGIN PRIVATE KEY-----\n" + pemBody + "\n-----END PRIVATE KEY-----"
	const bearer = "FuzzBearerCanary0123456789abcdef"

	f.Fuzz(func(t *testing.T, pre, post string) {
		for _, in := range []string{pre + post, pre + pem + post, pre + "Authorization: Bearer " + bearer + post} {
			once := String(in)
			if twice := String(once); twice != once {
				t.Fatalf("String is not idempotent on %q:\nonce:  %q\ntwice: %q", in, once, twice)
			}
		}
		if out := String(pre + pem + post); strings.Contains(out, pemBody) {
			t.Fatalf("PEM body survived String(%q): %q", pre+pem+post, out)
		}
		in := pre + "Authorization: Bearer " + bearer + post
		if out := String(in); strings.Contains(out, bearer) {
			t.Fatalf("bearer token survived String(%q): %q", in, out)
		}
	})
}
