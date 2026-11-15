package transport

import (
	"fmt"
	"net/http"
)

// HTTPSOnly returns a RoundTripper that refuses every request whose URL is not
// https before it reaches inner (http.DefaultTransport when nil). A client
// re-enters its Transport on each redirect hop, so the refusal covers a
// redirect to another scheme as well as the first URL.
func HTTPSOnly(inner http.RoundTripper) http.RoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return httpsOnly{inner: inner}
}

type httpsOnly struct{ inner http.RoundTripper }

func (t httpsOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, &InsecureURLError{URL: req.URL.Redacted()}
	}
	return t.inner.RoundTrip(req)
}

// InsecureURLError is HTTPSOnly refusing a request. It exits 1, not the
// network's retry-safe 50: the same URL would be refused again.
type InsecureURLError struct{ URL string }

func (e *InsecureURLError) Error() string {
	return fmt.Sprintf("refusing non-https URL %s", e.URL)
}

func (e *InsecureURLError) ExitCode() int { return 1 }
