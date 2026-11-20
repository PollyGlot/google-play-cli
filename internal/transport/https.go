package transport

import (
	"fmt"
	"net/http"
)

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

type InsecureURLError struct{ URL string }

func (e *InsecureURLError) Error() string {
	return fmt.Sprintf("refusing non-https URL %s", e.URL)
}

func (e *InsecureURLError) ExitCode() int { return 1 }
