package testkit

import (
	"bytes"
	"io"
	"mime"
	"net/http"
)

type RoundTripFunc func(*http.Request) (*http.Response, error)

func (f RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "application/json" && r.Body != nil && r.Body != http.NoBody {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		c := Call{Method: r.Method, Host: r.URL.Host, Path: r.URL.Path, URL: r.URL.String(), Body: body}
		if why := refusal(c, nil); why != "" {
			panic(why)
		}
	}
	return f(r)
}
